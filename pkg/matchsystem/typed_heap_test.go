package matchsystem

import "testing"

type typedHeapRegressionEntry struct {
	value int
	index int
}

var typedHeapRegressionOperations = typedHeapOperations[*typedHeapRegressionEntry]{
	less: func(left, right *typedHeapRegressionEntry) bool {
		return left.value < right.value
	},
	swap: func(values []*typedHeapRegressionEntry, i, j int) {
		values[i], values[j] = values[j], values[i]
		values[i].index = i
		values[j].index = j
	},
	setIndex: func(value *typedHeapRegressionEntry, index int) {
		value.index = index
	},
}

func TestTypedHeapPreservesOrderingIndexesAndTailClearing(t *testing.T) {
	entries := []*typedHeapRegressionEntry{
		{value: 4},
		{value: 1},
		{value: 3},
		{value: 2},
	}
	typedHeapInit(&entries, typedHeapRegressionOperations)
	assertTypedHeapRegressionIndexes(t, entries)
	if entries[0].value != 1 {
		t.Fatalf("Init root value=%d, want 1", entries[0].value)
	}

	entries[0].value = 9
	typedHeapFix(&entries, 0, typedHeapRegressionOperations)
	if entries[0].value != 2 {
		t.Fatalf("Fix reordered heap incorrectly: root=%d", entries[0].value)
	}
	assertTypedHeapRegressionIndexes(t, entries)

	removed := typedHeapRemove(&entries, 1, typedHeapRegressionOperations)
	if removed.value != 4 || removed.index != -1 {
		t.Fatalf("Remove returned value=%d index=%d, want value=4 index=-1", removed.value, removed.index)
	}
	assertTypedHeapRegressionIndexes(t, entries)

	backing := entries[:cap(entries)]
	popped := typedHeapPop(&entries, typedHeapRegressionOperations)
	if popped.value != 2 || popped.index != -1 {
		t.Fatalf("Pop returned value=%d index=%d, want value=2 index=-1", popped.value, popped.index)
	}
	if backing[len(entries)] != nil {
		t.Fatal("Pop retained the removed pointer in the backing array")
	}
	assertTypedHeapRegressionIndexes(t, entries)
}

func assertTypedHeapRegressionIndexes(t *testing.T, entries []*typedHeapRegressionEntry) {
	t.Helper()
	for index, entry := range entries {
		if entry.index != index {
			t.Fatalf("entry value=%d index=%d, want %d", entry.value, entry.index, index)
		}
	}
}
