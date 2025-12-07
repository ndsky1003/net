package ut

func ResolveOption[T any](old **T, new *T) {
	if new != nil {
		*old = new
	}
}
