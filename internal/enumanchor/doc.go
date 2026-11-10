// Package enumanchor holds no production code: its test pins every Go list
// copied from a Discovery enum (a path segment, a value the CLI sends, a
// state it switches on) to the enum of the embedded Schema index
// (schemaindex.Embedded), so a snapshot refresh that adds, renames or removes
// a value turns CI red.
//
// It exists because of #758. Google had removed promoGraphic from
// AppImageType before the first committed snapshot, yet internal/play/images
// kept it in a hand-written table and every metadata images command hit
// Play's 400. The Discovery routine diffs one snapshot against the next, so it
// never compared the code with the snapshot.
//
// To anchor a new list, make its values reachable from outside its package
// (exported constants, a var or a function), then add one row to the table in
// enumanchor_test.go: the source in the index (a schema property, or a
// parameter on one or more methods), the values built from those exported
// symbols, and, when the code deliberately leaves a Discovery value aside,
// that value with the reason in unhandled. The row type documents the exact
// and subset modes. Only scalar properties and method parameters carry an
// enum in the index, never array items.
package enumanchor
