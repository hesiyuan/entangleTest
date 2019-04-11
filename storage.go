package main

import (
	sql "database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

// seqVector entry declaration
type seqVEntry struct {
	Clock uint64
	Dirty bool // dirty flag indicates whether runtime DS has this entry changed since last writs to table
}

// sequence vector maintain logical clocks of last received operation from every peers
// including itself. Assumming all logical clocks start at 0.
// the content of the seqVector will need to be stored to disk. (what happens if crashed without saving?)
// (Ans: can be by operation log and the Sync protocol)
// Moved from connection.go into this file
// TODO: may need to cleanup the initilization in connection.go
// Currently, seqVector is initialized in connection.go
var seqVector map[string]*seqVEntry

// operations database handle. Long lived handle
var opsdb *sql.DB // from "database/sql"

// document database handle. Long lived
var docdb *sql.DB

// long-lived Statement for local operations insert
var Stmt *sql.Stmt

// char insert statement
var docInsertStmt *sql.Stmt

// char delete statement
var docDeleteStmt *sql.Stmt

// the docdbID of very last inserted char. Protected by a lock
type docdbID struct {
	value uint64
	mux   sync.Mutex
}

var lastdocdbID docdbID

// This only init Ops storage
func InitStorage() {
	//createSeqVStorage()
	CreateOpsStorage()
	createDocStorage()
}

// This function creates operations storage and prepares a statement for (insert/delete)
// Each operation in the table is a tuple <Atom, operation, clock, Pos>
// clock is the primary key
// Current implementation assumes each client is with a single ops table
// It only supports a single file. Note that opsdb will remain open
func CreateOpsStorage() {
	//Open is used to create a database handle
	path := "./ops" + clientID + ".db"
	var err error
	// createFlag indicates whether to create a ops table
	createFlag := true

	// need to check whether the local Ops exists, set createFlag to false if so
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		// they may be situation that file exists but not table, we suppose this is unlikely
		createFlag = false
	}

	opsdb, err = sql.Open("sqlite3", path)
	if err != nil {
		log.Fatal(err)
	}
	//It is rare to Close a DB, as the DB handle is meant to be long-lived and shared between many goroutines.
	//defer db.Close()

	if createFlag == true {
		sqlStmt := `
		create table ops (
			 clock integer not null primary key,
			 atom text,
			 operation integer,
			 posIdentifier blob
			 );
		delete from ops;
		`
		_, err = opsdb.Exec(sqlStmt)
		if err != nil {
			log.Printf("%q: %s\n", err, sqlStmt)
			return
		}
	}

	Stmt, err = opsdb.Prepare("insert into ops(clock, atom, operation, posIdentifier) values(?, ?, ?, ?)")

	if err != nil {
		log.Fatal(err)
	}

	// do not close the Stmt yet, as it will be used over and over again
}

// This function creates Doc storage representing the underlying document.
func createDocStorage() {
	//Open is used to create a database handle
	path := "./doc" + clientID + ".db"
	var err error
	// createFlag indicates whether to create a doc table
	createFlag := true

	// need to check whether the doc exists, set createFlag to false if so
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		// they may be situation that file exists but not table, we suppose this is unlikely
		createFlag = false
	}

	docdb, err = sql.Open("sqlite3", path)
	if err != nil {
		log.Fatal(err)
	}
	//It is rare to Close a DB, as the DB handle is meant to be long-lived and shared between many goroutines.
	//defer db.Close()

	if createFlag == true {
		sqlStmt := `
		create table doc (
			 id integer not null primary key,
			 atom text,
			 posIdentifier blob
			 );
		delete from doc;
		`
		_, err = docdb.Exec(sqlStmt)
		if err != nil {
			log.Printf("%q: %s\n", err, sqlStmt)
			return
		}
	}

	docInsertStmt, err = docdb.Prepare("insert into doc(id, atom, posIdentifier) values(?, ?, ?)")

	docDeleteStmt, err = docdb.Prepare("delete from doc where id = ?")

	if err != nil {
		log.Fatal(err)
	}

	if createFlag == false {
		// load the lastdocID as well, thread-safe at this point
		loadLastDocID(&lastdocdbID.value)

	} else {
		// setting lastdocdbID to 0 as we are creating a new doc
		// inserting begin and End if we are creating a new document
		docInsertStmt.Exec(0, "", PosBytes(Start)) // Start
		docInsertStmt.Exec(1, "", PosBytes(End))   // End

		lastdocdbID.value = 1
	}

	// do not close the Stmt yet, as it will be used over and over again

}

// load the id of the very last inserted char
// assumming id is incrementing, the last id is the max id
// Pre: docDB hanble must be open
func loadLastDocID(id *uint64) {
	// LastID may need to be changed
	rows, err := docdb.Query("select MAX(id) as LastID from doc")

	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() { // only iterate once
		err = rows.Scan(id)
		if err != nil {
			log.Fatal(err)
		}
	}

	err = rows.Err()
	if err != nil {
		log.Fatal(err)
	}

}

// Insert a char to the docDB
func InsertCharToDocDB(id uint64, atom string, posIdentifier []Identifier) error {
	// convert pos to bytes array
	posBytes := PosBytes(posIdentifier)

	_, err := docInsertStmt.Exec(id, atom, posBytes)
	if err != nil {
		log.Fatal(err)
		return errors.New("unable to write to char to docDB")
	}

	return nil

}

// Delete a char from the docDB
func DeleteCharFromDocDB(id uint64) error {

	_, err := docDeleteStmt.Exec(id)
	if err != nil {
		log.Fatal(err)
		return errors.New("unable to delete a char from docDB")
	}

	return nil

}

// NextDoc returns the next available char ID and advance the last inserted id
// protected by a lock
func NextDocID() uint64 {
	lastdocdbID.mux.Lock()
	lastdocdbID.value = lastdocdbID.value + 1
	lastdocdbID.mux.Unlock()
	return lastdocdbID.value
}

// obvious as its name suggests
func GetDocID() uint64 {
	return lastdocdbID.value
}

// NewDocument loads from docdb and insert all chars into CRDT document
// New creates a new Document containing the given content and a clientID
func LoadDocument(clientID uint8) *Document {
	d := &Document{clientID: clientID} // local variable? stored in stack?
	// Note that, unlike in C, it's perfectly OK to return the address of a local variable;
	// the storage associated with the variable survives after the function returns.

	// select all from docdb database and insert using binary search
	rows, err := docdb.Query("select id, atom, posIdentifier from doc")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() { // should be sorted by id already
		var ID uint64
		var atom string
		var posIdentifier []byte
		err = rows.Scan(&ID, &atom, &posIdentifier)
		if err != nil {
			log.Fatal(err)
		}

		d.insert(NewPos(posIdentifier), atom, ID)
	}

	err = rows.Err()
	if err != nil {
		log.Fatal(err)
	}

	return d
}

func writeOpToStorage(value string, OpType bool, clock uint64, pos []Identifier) error {

	// convert pos to bytes array
	posBytes := PosBytes(pos)

	_, err := Stmt.Exec(clock, value, OpType, posBytes)
	if err != nil {
		log.Fatal(err)
		return errors.New("unable to write to ops table")
	}

	return nil
}

// Given seqVector in runtime, the function creates seqV storage in file system
// If the table does not exist and that
// If the file currently being edited has no name, _seqV.db will be created.
// If the file has name <filename>, <filename>_seqV.db will be created.
// For now, let's be simple, just name it to be seqV.db
// Note: This func has not been tested
func createSeqVStorage() {

	// suffix := "_seqV.db"
	// prefix := "./"

	// path := prefix + CurView().Buf.name + suffix
	path := "./seqV" + clientID + ".db"
	// make seqVector
	makeSeqVector()
	// need to check whether the file exists, return immediately if so
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		// file does exist, load the table into SeqVector
		loadStorageIntoSeqVector() // TODO: table may not be fully created. TODO: peridocic saving here as well
		return
	}

	// The file does not exist, this is a new file
	// create a seqVector table associated with the file
	db, err := sql.Open("sqlite3", path) // a potential issues is corruption with the database
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// creating the table
	sqlStmt := `
	create table seqV (
		 clientID text not null primary key,
		 clock integer
		 );
	delete from seqV;
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		log.Printf("%q: %s\n", err, sqlStmt)
		return
	}

	//loadStorageIntoSeqVector()
	// update is false, since it is the first time
	SeqVectorToStorage(false)

	// periodically saving SeqVector back to storage
	// current period is set to 10s for testing. deployment time may be longer TODO:
	// a dirty flag is also used to prevent unnecessary saving

}

// This function saves seqVector back to storage. If it is the first time,
// records will be created. If the seqV exists, all entries will be updated
// All dirty flags will be cleared as well
// The flag passed into it dertermines to insert or update entries
func SeqVectorToStorage(update bool) {
	path := "./seqV" + clientID + ".db"

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	tx, err := db.Begin() // transaction
	if err != nil {
		log.Fatal(err)
	}

	var statement *sql.Stmt
	// if update all entries
	if update == true {
		statement, err = tx.Prepare("update seqV set clock = ? where clientID = ?")

	} else {
		statement, err = tx.Prepare("insert into seqV(clientID, clock) values(?, ?)")
	}

	if err != nil {
		log.Fatal(err)
	}
	defer statement.Close()

	// iterating over the map
	// Note: it is assumed seqVector DS has been created
	for clientK, entry := range seqVector {
		if update == true {
			if entry.Dirty == false { // do not update unchanged entry
				continue
			}
			_, err = statement.Exec(entry.Clock, clientK) // update
			entry.Dirty = false                           // clear flag
		} else {
			_, err = statement.Exec(clientK, entry.Clock) // insert
		}

		if err != nil {
			log.Fatal(err)
		}
	}
	// nothing to commit would give an error?
	tx.Commit()
}

// This function resets SeqVector to 0s
func makeSeqVector() {
	// initialize peerAddresses and seqVector first
	for i := range peerAddresses {
		seqVector[peerAddresses[i]] = &seqVEntry{0, false}
	}

}

// This function loads the storage into runtime seqVector.
// This should be called once when entangleText launchs
// Pre: seqVector DS must exist.
// Note: This func has not been tested
func loadStorageIntoSeqVector() {
	path := "./seqV" + clientID + ".db"
	//os.Remove(path) // TODO:

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// query
	rows, err := db.Query("select clientID, clock from seqV")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	counter := uint8(0)

	for rows.Next() {
		var clientID string
		var clock uint64
		err = rows.Scan(&clientID, &clock)
		if err != nil {
			log.Fatal(err)
		}
		seqVector[clientID].Clock = clock // assignment
		counter = counter + 1
	}

	if counter != numPeers {
		err := errors.New("SeqVector table corrupted")
		fmt.Print(err)
	}

	err = rows.Err()
	if err != nil {
		log.Fatal(err)
	}

}

// This function select operations between receiverClock and localClock
// In this minimum where they are equal, the return value contains one operation
func ExtractOperationsBetween(ReceiverClock, localClock uint64) (patch []Operation) {
	// path := "./seqV" + clientID + ".db"

	// db, err := sql.Open("sqlite3", path)
	// if err != nil {
	// 	log.Fatal(err)
	// }
	// defer db.Close()
	// query on opsdb directly
	rows, err := opsdb.Query("select * from ops where clock between ? and ?", ReceiverClock, localClock) // select by range
	if err != nil {
		log.Fatal(err)
	} // as long as there’s an open result set (represented by rows), the underlying connection is busy and can’t be used for any other query.
	defer rows.Close() //We defer rows.Close(). This is very important.

	patch = make([]Operation, localClock-ReceiverClock+1)
	index := 0

	for rows.Next() {
		err = rows.Scan(&patch[index].Clock,
			&patch[index].Atom,
			&patch[index].OpType,
			&patch[index].Pos) // this obtains data
		if err != nil { // If there’s an error during the loop, you need to know about it.
			log.Fatal(err)
		}
		index = index + 1
	}
	err = rows.Err()
	if err != nil {
		log.Fatal(err)
	}

	return patch
}
