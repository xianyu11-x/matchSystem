package prefilter

import (
	"fmt"

	"github.com/RoaringBitmap/roaring/v2"

	"matchSystem/internal/common"
)

type stringIndex struct {
	spec      indexSpec
	postings  map[string]*roaring.Bitmap
	keysByDoc map[uint32][]string
}

func newMultiValueIndex(spec indexSpec) runtimeIndex {
	if spec.keyType == KeyTypeUint64 {
		return &uint64Index{spec: spec, postings: make(map[uint64]*roaring.Bitmap), keysByDoc: make(map[uint32][]uint64)}
	}
	return &stringIndex{spec: spec, postings: make(map[string]*roaring.Bitmap), keysByDoc: make(map[uint32][]string)}
}

func (i *stringIndex) keys(ticket *common.Ticket) []string {
	return uniqueStrings(ticket.StringLists[i.spec.field])
}
func (i *stringIndex) validate(ticket *common.Ticket) error {
	keys := i.keys(ticket)
	if len(keys) > i.spec.maxDocumentValues {
		return fmt.Errorf("index %q field %q produced %d document values; maximum is %d", i.spec.name, i.spec.field, len(keys), i.spec.maxDocumentValues)
	}
	return nil
}
func (i *stringIndex) add(docID uint32, ticket *common.Ticket) {
	keys := i.keys(ticket)
	if len(keys) == 0 {
		return
	}
	i.keysByDoc[docID] = keys
	for _, key := range keys {
		posting := i.postings[key]
		if posting == nil {
			posting = roaring.New()
			i.postings[key] = posting
		}
		posting.Add(docID)
	}
}
func (i *stringIndex) remove(docID uint32) {
	for _, key := range i.keysByDoc[docID] {
		posting := i.postings[key]
		if posting == nil {
			continue
		}
		posting.Remove(docID)
		if posting.IsEmpty() {
			delete(i.postings, key)
		}
	}
	delete(i.keysByDoc, docID)
}
func (*stringIndex) prepare() {}

func (i *stringIndex) estimate(query boundIndexQuery) (uint64, error) {
	q, ok := query.(boundStringQuery)
	if !ok {
		return 0, fmt.Errorf("string multi-value index received incompatible query")
	}
	var estimate uint64
	for _, key := range q.keys {
		if posting := i.postings[key]; posting != nil {
			estimate += posting.GetCardinality()
		}
	}
	return estimate, nil
}
func (i *stringIndex) lookup(query boundIndexQuery) (*roaring.Bitmap, error) {
	q, ok := query.(boundStringQuery)
	if !ok {
		return nil, fmt.Errorf("string multi-value index received incompatible query")
	}
	out := roaring.New()
	for _, key := range q.keys {
		if posting := i.postings[key]; posting != nil {
			out.Or(posting)
		}
	}
	return out, nil
}
func (i *stringIndex) contains(query boundIndexQuery, docID uint32) (bool, error) {
	q, ok := query.(boundStringQuery)
	if !ok {
		return false, fmt.Errorf("string multi-value index received incompatible query")
	}
	for _, key := range q.keys {
		if posting := i.postings[key]; posting != nil && posting.Contains(docID) {
			return true, nil
		}
	}
	return false, nil
}

type uint64Index struct {
	spec      indexSpec
	postings  map[uint64]*roaring.Bitmap
	keysByDoc map[uint32][]uint64
}

func (i *uint64Index) keys(ticket *common.Ticket) []uint64 {
	return uniqueUint64s(ticket.Uint64Lists[i.spec.field])
}
func (i *uint64Index) validate(ticket *common.Ticket) error {
	keys := i.keys(ticket)
	if len(keys) > i.spec.maxDocumentValues {
		return fmt.Errorf("index %q uint64 field %q produced %d document values; maximum is %d", i.spec.name, i.spec.field, len(keys), i.spec.maxDocumentValues)
	}
	return nil
}
func (i *uint64Index) add(docID uint32, ticket *common.Ticket) {
	keys := i.keys(ticket)
	if len(keys) == 0 {
		return
	}
	i.keysByDoc[docID] = keys
	for _, key := range keys {
		posting := i.postings[key]
		if posting == nil {
			posting = roaring.New()
			i.postings[key] = posting
		}
		posting.Add(docID)
	}
}
func (i *uint64Index) remove(docID uint32) {
	for _, key := range i.keysByDoc[docID] {
		posting := i.postings[key]
		if posting == nil {
			continue
		}
		posting.Remove(docID)
		if posting.IsEmpty() {
			delete(i.postings, key)
		}
	}
	delete(i.keysByDoc, docID)
}
func (*uint64Index) prepare() {}

func (i *uint64Index) estimate(query boundIndexQuery) (uint64, error) {
	q, ok := query.(boundUint64Query)
	if !ok {
		return 0, fmt.Errorf("uint64 multi-value index received incompatible query")
	}
	var estimate uint64
	for _, key := range q.keys {
		if posting := i.postings[key]; posting != nil {
			estimate += posting.GetCardinality()
		}
	}
	return estimate, nil
}
func (i *uint64Index) lookup(query boundIndexQuery) (*roaring.Bitmap, error) {
	q, ok := query.(boundUint64Query)
	if !ok {
		return nil, fmt.Errorf("uint64 multi-value index received incompatible query")
	}
	out := roaring.New()
	for _, key := range q.keys {
		if posting := i.postings[key]; posting != nil {
			out.Or(posting)
		}
	}
	return out, nil
}
func (i *uint64Index) contains(query boundIndexQuery, docID uint32) (bool, error) {
	q, ok := query.(boundUint64Query)
	if !ok {
		return false, fmt.Errorf("uint64 multi-value index received incompatible query")
	}
	for _, key := range q.keys {
		if posting := i.postings[key]; posting != nil && posting.Contains(docID) {
			return true, nil
		}
	}
	return false, nil
}
