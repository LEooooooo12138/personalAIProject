//go:build windows

package assistantconfig

import (
	"bytes"
	"golang.org/x/sys/windows"
	"unsafe"
)

const supportsVoiceReference = true

const caseInsensitivePaths = true

var encryptedPrefix = []byte("assistant-dpapi-v1\x00")

func sealRecord(plain []byte, _ bool) ([]byte, error) {
	in := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	encrypted := append([]byte{}, encryptedPrefix...)
	return append(encrypted, unsafe.Slice(out.Data, int(out.Size))...), nil
}
func unsealRecord(data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, encryptedPrefix) || len(data) <= len(encryptedPrefix) {
		return nil, ErrUnavailable
	}
	data = data[len(encryptedPrefix):]
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	result := append([]byte{}, unsafe.Slice(out.Data, int(out.Size))...)
	clear(unsafe.Slice(out.Data, int(out.Size)))
	return result, nil
}
func restrictPath(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}
func rejectReparse(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		return err
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return ErrUnavailable
	}
	return nil
}
