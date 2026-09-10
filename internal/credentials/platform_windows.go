package credentials

func platformProtection() (func([]byte) ([]byte, error), func([]byte) ([]byte, error)) {
	return protect, unprotect
}
