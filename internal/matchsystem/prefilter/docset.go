package prefilter

import "github.com/RoaringBitmap/roaring/v2"

// DocSet is an owned mutable set of uint32 DocIDs.
type DocSet struct{ data *roaring.Bitmap }

func NewDocSet(ids ...uint32) *DocSet {
	b := roaring.New()
	b.AddMany(ids)
	return &DocSet{data: b}
}

func wrapDocSet(data *roaring.Bitmap) *DocSet {
	if data == nil {
		data = roaring.New()
	}
	return &DocSet{data: data}
}

func (b *DocSet) ensure() *roaring.Bitmap {
	if b.data == nil {
		b.data = roaring.New()
	}
	return b.data
}

func (b *DocSet) Add(id uint32)           { b.ensure().Add(id) }
func (b *DocSet) Remove(id uint32)        { b.ensure().Remove(id) }
func (b *DocSet) Contains(id uint32) bool { return b != nil && b.data != nil && b.data.Contains(id) }
func (b *DocSet) Count() uint64 {
	if b == nil || b.data == nil {
		return 0
	}
	return b.data.GetCardinality()
}
func (b *DocSet) IsEmpty() bool { return b == nil || b.data == nil || b.data.IsEmpty() }
func (b *DocSet) Clone() *DocSet {
	if b == nil || b.data == nil {
		return NewDocSet()
	}
	return wrapDocSet(b.data.Clone())
}
func (b *DocSet) Subtract(other *DocSet) {
	if b != nil && other != nil && other.data != nil {
		b.ensure().AndNot(other.data)
	}
}

// IDs materializes the DocIDs in ascending order.
func (b *DocSet) IDs() []uint32 {
	if b == nil || b.data == nil {
		return nil
	}
	return b.data.ToArray()
}

// ForEach visits DocIDs in ascending order until visit returns false.
func (b *DocSet) ForEach(visit func(uint32) bool) {
	if b == nil || b.data == nil || visit == nil {
		return
	}
	it := b.data.Iterator()
	for it.HasNext() {
		if !visit(it.Next()) {
			return
		}
	}
}
