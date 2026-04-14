package cli

// ExitError carries an exit code and whether the error should be suppressed on stderr.
type ExitError struct {
	Code   int
	Silent bool
	Err    error
}

func (e *ExitError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func silentExit(code int, err error) error {
	return &ExitError{
		Code:   code,
		Silent: true,
		Err:    err,
	}
}
