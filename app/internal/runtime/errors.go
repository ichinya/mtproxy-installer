package runtime

import (
	"errors"
	"fmt"
)

type ErrorCode string

const (
	CodeInstallDirMissing    ErrorCode = "install_dir_missing"
	CodeInstallDirInvalid    ErrorCode = "install_dir_invalid"
	CodeRequiredFileMissing  ErrorCode = "required_file_missing"
	CodeEnvParse             ErrorCode = "env_parse"
	CodeProviderUndetected   ErrorCode = "provider_undetected"
	CodeProviderUnsupported  ErrorCode = "provider_unsupported"
	CodeProviderAmbiguous    ErrorCode = "provider_ambiguous"
	CodeProviderMismatch     ErrorCode = "provider_mismatch"
	CodePermissionDenied     ErrorCode = "permission_denied"
	CodeInstallDirUnreadable ErrorCode = "install_dir_unreadable"
)

type RuntimeError struct {
	Code     ErrorCode
	Path     string
	Field    string
	Provider string
	Message  string
	Err      error
}

func (e *RuntimeError) Error() string {
	if e == nil {
		return "runtime error"
	}

	switch {
	case e.Path != "" && e.Message != "":
		return fmt.Sprintf("%s (%s)", e.Message, e.Path)
	case e.Path != "":
		return fmt.Sprintf("runtime error (%s)", e.Path)
	case e.Message != "":
		return e.Message
	default:
		return "runtime error"
	}
}

func (e *RuntimeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *RuntimeError) Is(target error) bool {
	if e == nil {
		return false
	}

	t, ok := target.(*RuntimeError)
	if !ok {
		return false
	}

	if t.Code != "" && e.Code != t.Code {
		return false
	}
	if t.Path != "" && e.Path != t.Path {
		return false
	}
	if t.Field != "" && e.Field != t.Field {
		return false
	}
	if t.Provider != "" && e.Provider != t.Provider {
		return false
	}
	return true
}

func isRuntimeErrorCode(err error, code ErrorCode) bool {
	if err == nil {
		return false
	}
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		return false
	}
	return runtimeErr.Code == code
}
