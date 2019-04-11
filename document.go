package main

import (
	"bytes"
	"math/rand"
)

// Adapted from Ravern Koh's implementation
// Document represents a Logoot Documentument. Actions like Insert and Delete can be performed
// on Document. If at any time an invalid position is given, a panic will occur, so raw
// positions should only be used for debugging purposes.
type Document struct {
	clientID uint8
	pairs    []pair
}

// Pos is an element of a position identifier. A position identifier identifies an
// atom within a Doc. The behaviour of an empty position identifier (length == 0) is
// undefined, so just do not pass in empty position identifiers to any method/function.
type Identifier struct {
	Ident uint16
	Site  uint8
}

// pair is a position identifier and its atom.
type pair struct {
	Pos     []Identifier // a position is a list of identifiers
	Atom    string       // this is actually a char, but stick to string for easy future extension to string-wise
	docdbID uint64       // unique constant as identified in the docdb
}

// Start and end positions. These will always exist within a Documentument.
var (
	Start = []Identifier{{0, 0}}
	End   = []Identifier{{^uint16(0), 0}}
)

/* Basic methods */

// Index of a position in the Document. Secondary value indicates whether the value exists.
// If the value doesn't exist, the index returned is the index that the position would
// have been in, should it have existed.
func (d *Document) Index(p []Identifier) (int, bool) {
	off := 0
	pr := d.pairs
	for {
		if len(pr) == 0 {
			return off, false
		}
		spt := len(pr) / 2 // binary search
		pair := pr[spt]
		if cmp := ComparePos(pair.Pos, p); cmp == 0 {
			return spt + off, true
		} else if cmp == -1 {
			off += spt + 1
			pr = pr[spt+1:]
		} else if cmp == 1 {
			pr = pr[0:spt]
		}
	}
}

// ComparePos compares two position identifiers, returning -1 if the left is less than the
// right, 0 if equal, and 1 if greater.
func ComparePos(lp []Identifier, rp []Identifier) int8 {
	for i := 0; i < len(lp); i++ {
		if len(rp) == i {
			return 1
		}
		if lp[i].Ident < rp[i].Ident {
			return -1
		}
		if lp[i].Ident > rp[i].Ident {
			return 1
		}
		if lp[i].Site < rp[i].Site {
			return -1
		}
		if lp[i].Site > rp[i].Site {
			return 1
		}
	}
	if len(rp) > len(lp) {
		return -1
	}
	return 0
}

// Atom at the position. Secondary return value indicates whether the value exists.
func (d *Document) Get(p []Identifier) (string, bool) {
	i, exists := d.Index(p)
	if !exists {
		return "", false
	}
	return d.pairs[i].Atom, true
}

// Insert a new pair at the position, returning success or failure (already existing
// position). Note that atom is a single byte to insert
func (d *Document) insert(p []Identifier, atom string, docdbID uint64) bool {
	i, exists := d.Index(p)
	if exists {
		return false
	}
	// this is harmful for rach condition; insert at position i
	d.pairs = append(d.pairs[0:i], append([]pair{{p, atom, docdbID}}, d.pairs[i:]...)...)
	return true
}

// Given a position identifier, inserts a byte array to the right of the given position
// Note that this may insert multiple bytes. And it is only local insert
// This function is not efficient, as insertRight calls insert which uses append
// will need to be rewritten to use only a single append
// RETURN: the pos identifier of the last inserted byte in the byte sequence
func (d *Document) insertMultiple(p []Identifier, value []byte, docdbID uint64) ([]Identifier, bool) {
	if len(value) < 1 {
		return nil, false
	}

	np, success := d.InsertRight(p, string(value[0]), docdbID)
	if !success {
		return nil, false
	}
	// CRDT treats every character as the same, no need to split on return
	// at this time, the following for loop won't be executed
	for i := 1; i < len(value); i++ { // go through each byte in value[]
		// notice that the 1st argument to InsertRight is now updated np
		np, success = d.InsertRight(np, string(value[i]), docdbID)
		if !success {
			return nil, false
		}
	}

	return np, true
}

// Delete the pair at the position, returning success or failure (non-existent position).
// return dbID as a convenience
func (d *Document) delete(p []Identifier) (bool, uint64) {
	i, exists := d.Index(p)
	if !exists || i == 0 || i == len(d.pairs)-1 {
		return false, 0
	}
	dbID := d.pairs[i].docdbID
	d.pairs = append(d.pairs[0:i], d.pairs[i+1:]...)
	return true, dbID
}

// Delete pairs starting at startIndex and up to endIndex
// Currently returns the position identifier and the dbID of the first deleted char
// later will need to construct a list of position identifiers deleted to be transmitted
func (d *Document) deleteMultiple(startIndex, endIndex int) ([]Identifier, uint64) {

	if startIndex == 0 || endIndex == len(d.pairs)+1 { // cannot delete Start and End
		return nil, 0
	}

	if startIndex == endIndex { // endIndex must be at least on higher than startIndex
		return nil, 0
	}

	pos := d.pairs[startIndex].Pos
	dbID := d.pairs[startIndex].docdbID
	d.pairs = append(d.pairs[0:startIndex], d.pairs[endIndex:]...) // eplisis unpacks the second slice
	return pos, dbID
}

// Left returns the position to the left of the given position, and a flag indicating
// whether it exists (when the given position is the start, there is no position to the
// left of it). Will be false if the given position is invalid. The Start pair is not
// considered as an actual pair.
func (d *Document) Left(p []Identifier) ([]Identifier, bool) {
	i, exists := d.Index(p)
	if !exists || i == 0 {
		return nil, false
	}
	return d.pairs[i-1].Pos, true
}

// Right returns the position to the right of the given position, and a flag indicating
// whether it exists (when the given position is the end, there is no position to the
// right of it). Will be false if the given position is invalid. The End pair is not
// considered as an actual pair.
func (d *Document) Right(p []Identifier) ([]Identifier, bool) {
	i, exists := d.Index(p)
	if !exists || i >= len(d.pairs)-1 {
		return nil, false
	}
	return d.pairs[i+1].Pos, true
}

// random number between x and y, where y is greater than x.
func random(x, y uint16) uint16 {
	return uint16(rand.Intn(int(y-x-1))) + 1 + x
}

// GeneratePos generates a new position identifier between the two positions provided.
// Secondary return value indicates whether it was successful (when the two positions
// are equal, or the left is greater than right, position cannot be generated).
// Later will be optimized if time permits to LSEQ
func GeneratePos(lp, rp []Identifier, site uint8) ([]Identifier, bool) {
	if ComparePos(lp, rp) != -1 { // lp should be less than rp
		return nil, false
	}
	p := []Identifier{}
	for i := 0; i < len(lp); i++ { // why len(lp)? could be len(rp)
		l := lp[i]
		r := rp[i]
		if l.Ident == r.Ident && l.Site == r.Site {
			p = append(p, Identifier{l.Ident, l.Site})
			continue
		}
		if d := r.Ident - l.Ident; d > 1 { // there are spaces in this level
			r := random(l.Ident, r.Ident)
			p = append(p, Identifier{r, site})
		} else if d == 1 { // no space in this level
			if site > l.Site { // if site is larger, TOTest:
				p = append(p, Identifier{l.Ident, site})
			} else if site < r.Site {
				p = append(p, Identifier{r.Ident, site})
			} else { // we now that rp[i] - lp[i] == 1 in this case, go through rest of lp to find a place to generate
				// if lp[i+1] does not exist, then min := 0, else
				min := uint16(0)
				if len(lp) > i+1 {
					min = lp[i+1].Ident // this should be lp[i+1].Ident
				} // TODO: edge case need to check if min = max - 1
				if len(lp) > len(rp) { // long lp, hard case
					min = lp[len(rp)].Ident
					// Super edge case
					// left  => {3 1} {65534 1}
					// right => {4 1}. some optimization can be made here, but stick it for now
					// In this case, 65534 can't be min, because no number is in between
					// it and MAX. So need to extend the positions further.
					if min == ^uint16(0)-1 { // maxium is 65535, no space in lp's last level
						r := random(0, ^uint16(0))
						p = append(p, Identifier{l.Ident, l.Site}) // append previous
						p = append(p, lp[len(rp):]...)
						p = append(p, Identifier{r, site})
						return p, true
					}
				} // lp is shorter
				r := random(min, ^uint16(0)) // if min = ^uint16(0) - 1, then, need to append one more
				p = append(p, Identifier{l.Ident, l.Site}, Identifier{r, site})
			}
		} else {
			if site > l.Site && site < r.Site {
				p = append(p, Identifier{l.Ident, site})
			} else {
				r := random(0, ^uint16(0))
				p = append(p, Identifier{l.Ident, l.Site}, Identifier{r, site})
			}
		}
		return p, true
	}
	if len(rp) > len(lp) { // easy case, make a random integer in new level
		r := random(0, rp[len(lp)].Ident)
		p = append(p, Identifier{r, site})
	}
	return p, true
}

// use this one when insert
// GeneratePos generates a new position identifier between the two positions provided.
// Secondary return value indicates whether it was successful (when the two positions
// are equal, or the left is greater than right, position cannot be generated).
func (d *Document) GeneratePos(lp []Identifier, rp []Identifier) ([]Identifier, bool) {
	return GeneratePos(lp, rp, d.clientID)
}

/* Convenience methods */

// InsertLeft inserts the atom to the left of the given position, returning the inserted
// position and whether it is successful (when the given position doesn't exist,
// InsertLeft won't do anything and return false).
func (d *Document) InsertLeft(p []Identifier, atom string, docdbID uint64) ([]Identifier, bool) {
	lp, success := d.Left(p)
	if !success {
		return nil, false
	}
	np, success := d.GeneratePos(lp, p)
	if !success {
		return nil, false
	}
	return np, d.insert(np, atom, docdbID)
}

// InsertRight inserts the atom to the right of the given position, returning the inserted
// position whether it is successful (when the given position doesn't exist, InsertRight
// won't do anything and return false).
func (d *Document) InsertRight(p []Identifier, atom string, docdbID uint64) ([]Identifier, bool) {
	rp, success := d.Right(p)
	if !success {
		return nil, false
	}
	np, success := d.GeneratePos(p, rp) // generate a position identifier between p and rp
	if !success {
		return nil, false
	}
	return np, d.insert(np, atom, docdbID)
}

// DeleteLeft deletes the atom to the left of the given position, returning whether it
// was successful (when the given position is the start, there is no position to the left
// of it).
func (d *Document) DeleteLeft(p []Identifier) (bool, uint64) {
	lp, success := d.Left(p)
	if !success {
		return false, 0
	}
	return d.delete(lp)
}

// DeleteRight deletes the atom to the right of the given position, returning whether it
// was successful (when the given position is the end, there is no position to the right
// of it).
func (d *Document) DeleteRight(p []Identifier) (bool, uint64) {
	rp, success := d.Right(p)
	if !success {
		return false, 0
	}
	return d.delete(rp)
}

// Content of the entire Documentument.
func (d *Document) Content() string {
	var b bytes.Buffer
	for i := 1; i < len(d.pairs)-1; i++ {
		b.WriteString(d.pairs[i].Atom)
	}
	return b.String()
}

// Other useful functions for serialization

// PosBytes returns the position as a byte slice.
func PosBytes(p []Identifier) []byte {
	b := []byte{byte(len(p))}
	for _, c := range p {
		b = append(b, byte(c.Ident>>8), byte(c.Ident), byte(c.Site))
	}
	return b
}

// NewPos returns a position from the bytes. It doesn't validate the byte slice, so only
// pass into it valid bytes.
func NewPos(b []byte) []Identifier {
	p := []Identifier{}
	for i := 0; i < int(b[0]); i++ {
		offset := i*3 + 1
		ident := uint16(b[offset])<<8 + uint16(b[offset+1])
		site := uint8(b[offset+2])
		p = append(p, Identifier{ident, site})
	}
	return p
}
