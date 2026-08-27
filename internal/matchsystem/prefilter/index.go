package prefilter

import (
	"fmt"

	"github.com/RoaringBitmap/roaring/v2"

	"matchSystem/internal/common"
	"matchSystem/internal/matchsystem/contract"
	"matchSystem/internal/matchsystem/fact"
)

type indexSpec struct {
	name                              string
	kind                              contract.IndexType
	keyType                           contract.KeyType
	maxDocumentValues, maxQueryValues int
}

func compileIndexSpec(spec contract.IndexSpec) indexSpec {
	return indexSpec{
		name: spec.Name, kind: spec.Type, keyType: spec.KeyType,
		maxDocumentValues: spec.MaxDocumentValues, maxQueryValues: spec.MaxQueryValues,
	}
}

type RequiredIndex struct {
	Name              string
	Type              contract.IndexType
	KeyType           contract.KeyType
	MaxDocumentValues int
	MaxQueryValues    int
}

type Requirements struct {
	Indexes    []RequiredIndex
	Facts      []fact.Spec
	Attributes []contract.AttributeSpec
}

type runtimeIndex interface {
	validate(*common.Ticket) error
	add(uint32, *common.Ticket)
	remove(uint32)
	prepare()
	estimate(boundIndexQuery) (uint64, error)
	lookup(boundIndexQuery) (*roaring.Bitmap, error)
	contains(boundIndexQuery, uint32) (bool, error)
}

func newIndex(spec indexSpec) runtimeIndex {
	switch spec.kind {
	case contract.IndexTypeMultiValue:
		return newMultiValueIndex(spec)
	case contract.IndexTypeInt64Range:
		return newInt64RangeIndex(spec)
	default:
		panic(fmt.Sprintf("unsupported compiled index kind %q", spec.kind))
	}
}
