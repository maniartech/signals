package signals

func New[T any]() Signal[T] {

	s := &signal[T]{}
	s.Reset()
	return s
}

func NewAsync[T any]() Signal[T] {
	s := signal[T]{
		async: true,
	}
	s.Reset() // Reset the signal
	return &s
}
