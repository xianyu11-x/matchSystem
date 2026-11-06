package matchsystem

// typedHeapOperations supplies the ordering and mutation hooks needed by the
// generic heap routines. The element type remains concrete at every call site;
// the type parameter is the only abstraction used by the helper.
type typedHeapOperations[T any] struct {
	less     func(left, right T) bool
	swap     func(values []T, i, j int)
	setIndex func(value T, index int)
}

func typedHeapInit[T any, S ~[]T](values *S, operations typedHeapOperations[T]) {
	items := []T(*values)
	if operations.setIndex != nil {
		for index, value := range items {
			operations.setIndex(value, index)
		}
	}
	for index := len(items)/2 - 1; index >= 0; index-- {
		typedHeapDown(items, index, len(items), operations)
	}
}

func typedHeapPush[T any, S ~[]T](values *S, value T, operations typedHeapOperations[T]) {
	*values = append(*values, value)
	items := []T(*values)
	index := len(items) - 1
	if operations.setIndex != nil {
		operations.setIndex(value, index)
	}
	typedHeapUp(items, index, operations)
}

func typedHeapPop[T any, S ~[]T](values *S, operations typedHeapOperations[T]) T {
	items := []T(*values)
	if len(items) == 0 {
		panic("typed heap is empty")
	}
	last := len(items) - 1
	operations.swap(items, 0, last)
	value := items[last]
	var zero T
	items[last] = zero
	*values = S(items[:last])
	if operations.setIndex != nil {
		operations.setIndex(value, -1)
	}
	typedHeapDown(items[:last], 0, last, operations)
	return value
}

func typedHeapRemove[T any, S ~[]T](values *S, index int, operations typedHeapOperations[T]) T {
	items := []T(*values)
	if index < 0 || index >= len(items) {
		panic("typed heap index out of range")
	}
	last := len(items) - 1
	if index != last {
		operations.swap(items, index, last)
	}
	value := items[last]
	var zero T
	items[last] = zero
	*values = S(items[:last])
	if operations.setIndex != nil {
		operations.setIndex(value, -1)
	}
	if index != last {
		if !typedHeapDown(items[:last], index, last, operations) {
			typedHeapUp(items[:last], index, operations)
		}
	}
	return value
}

func typedHeapFix[T any, S ~[]T](values *S, index int, operations typedHeapOperations[T]) {
	items := []T(*values)
	if index < 0 || index >= len(items) {
		panic("typed heap index out of range")
	}
	if !typedHeapDown(items, index, len(items), operations) {
		typedHeapUp(items, index, operations)
	}
}

func typedHeapUp[T any](items []T, index int, operations typedHeapOperations[T]) {
	for index > 0 {
		parent := (index - 1) / 2
		if !operations.less(items[index], items[parent]) {
			break
		}
		operations.swap(items, index, parent)
		index = parent
	}
}

func typedHeapDown[T any](items []T, index, limit int, operations typedHeapOperations[T]) bool {
	moved := false
	for {
		child := index*2 + 1
		if child >= limit {
			break
		}
		if right := child + 1; right < limit && operations.less(items[right], items[child]) {
			child = right
		}
		if !operations.less(items[child], items[index]) {
			break
		}
		operations.swap(items, index, child)
		index = child
		moved = true
	}
	return moved
}
