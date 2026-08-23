package prefilter

import (
	"fmt"

	"github.com/RoaringBitmap/roaring/v2"
)

type IndexType string

type KeyType string

const (
	IndexTypeMultiValue IndexType = "multi-value"
	IndexTypeInt64Range IndexType = "int64-range"

	KeyTypeString KeyType = "string"
	KeyTypeUint64 KeyType = "uint64"
)

type MultiValueIndexConfig struct {
	Name              string
	Field             string
	KeyType           KeyType
	MaxDocumentValues int
	MaxQueryValues    int
}

type Int64RangeIndexConfig struct{ Name, Field string }

// IndexSpec is a closed physical index declaration.
type IndexSpec interface{ indexSpec() indexSpec }

type multiValueIndexSpec struct{ config MultiValueIndexConfig }
type int64RangeIndexSpec struct{ config Int64RangeIndexConfig }

func NewMultiValueIndex(config MultiValueIndexConfig) IndexSpec {
	return multiValueIndexSpec{config: config}
}
func NewInt64RangeIndex(config Int64RangeIndexConfig) IndexSpec {
	return int64RangeIndexSpec{config: config}
}
func (f multiValueIndexSpec) indexSpec() indexSpec {
	cfg := f.config
	if cfg.MaxDocumentValues == 0 {
		cfg.MaxDocumentValues = 64
	}
	if cfg.MaxQueryValues == 0 {
		cfg.MaxQueryValues = 64
	}
	if cfg.KeyType == "" {
		cfg.KeyType = KeyTypeString
	}
	return indexSpec{name: cfg.Name, field: cfg.Field, kind: IndexTypeMultiValue, keyType: cfg.KeyType, maxDocumentValues: cfg.MaxDocumentValues, maxQueryValues: cfg.MaxQueryValues}
}
func (f int64RangeIndexSpec) indexSpec() indexSpec {
	return indexSpec{name: f.config.Name, field: f.config.Field, kind: IndexTypeInt64Range}
}

type indexSpec struct {
	name, field                       string
	kind                              IndexType
	keyType                           KeyType
	maxDocumentValues, maxQueryValues int
}

type RequiredIndex struct {
	Name              string
	Field             string
	Type              IndexType
	KeyType           KeyType
	MaxDocumentValues int
	MaxQueryValues    int
}

type Requirements struct {
	Indexes []RequiredIndex
	Facts   []FactSpec
}

type runtimeIndex interface {
	validate(Document) error
	add(Document)
	remove(uint32)
	prepare()
	estimate(boundIndexQuery) (uint64, error)
	lookup(boundIndexQuery) (*roaring.Bitmap, error)
	contains(boundIndexQuery, uint32) (bool, error)
}

func newIndex(spec indexSpec) runtimeIndex {
	switch spec.kind {
	case IndexTypeMultiValue:
		return newMultiValueIndex(spec)
	case IndexTypeInt64Range:
		return newInt64RangeIndex(spec)
	default:
		panic(fmt.Sprintf("unsupported compiled index kind %q", spec.kind))
	}
}
