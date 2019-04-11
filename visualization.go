package main

import "runtime"

// This function hightlights patches
func HighlightPatch(patch []Operation) {

	buf := CurView().Buf
	for _, op := range patch { // we already inserted, so they all exists
		if op.OpType == true { // does not care about deleted changes for now
			posIdentifier := NewPos(op.Pos)
			CRDTIndex, _ := buf.Document.Index(posIdentifier)
			// converting CRDTIndex to lineArray pos
			LinePos := FromCharPos(CRDTIndex-1, buf) // off by 1
			//screen.SetContent(LinePos.X+1, LinePos.Y, []rune(op.Atom)[0], nil, GetColor("green")) // set all empty?
			screen.SetContent(LinePos.X, LinePos.Y, []rune(op.Atom)[0], nil, GetColor("green")) // set all empty?
		}
	}
	// just testing
	screen.SetContent(5, 5, rune(50), nil, GetColor("red")) // set all empty?
	//screen.Show()
}

// RedrawAll redraws everything -- all the views and the messenger
// And highlight recently obtained patch as well
func RedrawAllWithPatchHighlight(patch []Operation) {
	messenger.Clear()
	// clear the screen first
	w, h := screen.Size()
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			screen.SetContent(x, y, ' ', nil, defStyle) // set all empty?
		}
	}

	for _, v := range tabs[curTab].Views { // draw all meat in each view
		v.Display()
	}
	DisplayTabs() // display all tabs if we have multiple
	messenger.Display()
	if globalSettings["keymenu"].(bool) {
		DisplayKeyMenu()
	}

	HighlightPatch(patch) // key call

	screen.Show() // make the SetContent call visible

	if numRedraw%50 == 0 {
		runtime.GC() // runtime garbage collection
	}
	numRedraw++
}
