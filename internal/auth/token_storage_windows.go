//go:build windows

package auth

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errUnsafeStore = errors.New("token storage requires a trusted owner, private file ACL and non-writable directory ACL; reparse points and unsupported ACEs are not supported")

func currentStoreUser() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, errUnsafeStore
	}
	return user.User.Sid, nil
}

func trustedStoreSID(sid, user *windows.SID) bool {
	return sid != nil && (sid.Equals(user) || sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid))
}

func validateStoreACL(sd *windows.SECURITY_DESCRIPTOR, directory bool) error {
	user, err := currentStoreUser()
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || !trustedStoreSID(owner, user) {
		return errUnsafeStore
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		return errUnsafeStore
	}
	// Allow untrusted read/traverse rights only on directories, never files.
	// Deny ACEs never cancel an unsafe allow: no effective-access or
	// group-membership approximation can accidentally grant trust here.
	var allowed windows.ACCESS_MASK
	if directory {
		allowed = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE | windows.GENERIC_READ | windows.GENERIC_EXECUTE
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil || ace == nil {
			return errUnsafeStore
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE && ace.Header.AceType != windows.ACCESS_DENIED_ACE_TYPE {
			return errUnsafeStore
		}
		const inheritanceFlags = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE | windows.NO_PROPAGATE_INHERIT_ACE | windows.INHERIT_ONLY_ACE | windows.INHERITED_ACE
		if ace.Header.AceFlags & ^uint8(inheritanceFlags) != 0 {
			return errUnsafeStore
		}
		// The OS validates descriptors; still bound the variable-length SID
		// before passing it to SID helpers. A basic SID needs at least 8 bytes.
		if ace.Header.AceSize < 16 {
			return errUnsafeStore
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() || sid.Len() > int(ace.Header.AceSize)-8 {
			return errUnsafeStore
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		if !trustedStoreSID(sid, user) && ace.Mask & ^allowed != 0 {
			return errUnsafeStore
		}
	}
	runtime.KeepAlive(sd)
	return nil
}

func validateStoreHandle(handle windows.Handle, directory bool) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory {
		return errUnsafeStore
	}
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return errUnsafeStore
	}
	return validateStoreACL(sd, directory)
}

func openStoreFile(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, errUnsafeStore
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

func openStoreLockFile(path string) (*os.File, error) {
	sd, err := privateStoreDescriptor()
	if err != nil {
		return nil, errUnsafeStore
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, errUnsafeStore
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	// Share read/write for other lock waiters, but never deletion of the lock
	// object. OPEN_ALWAYS preserves existing ACLs, which the caller validates.
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, &sa, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	runtime.KeepAlive(sd)
	if err != nil {
		return nil, errUnsafeStore
	}
	return os.NewFile(uintptr(h), path), nil
}

func tryStoreLock(f *os.File) (bool, error) {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func privateStoreFile(f *os.File) error {
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errUnsafeStore
	}
	return validateStoreHandle(windows.Handle(f.Fd()), false)
}

func validateStoreReplacement(path string) error {
	f, err := openStoreFile(path)
	if err != nil {
		return errUnsafeStore
	}
	defer func() { _ = f.Close() }()
	return privateStoreFile(f)
}

func privateStoreDir(dir string) error {
	// Reject junctions and other reparse points in every existing component,
	// not just Go's symlink subset. No existing directory ACL is modified.
	abs, err := filepath.Abs(dir)
	if err != nil {
		return errUnsafeStore
	}
	for p := abs; ; p = filepath.Dir(p) {
		name, err := windows.UTF16PtrFromString(p)
		if err != nil {
			return errUnsafeStore
		}
		attrs, err := windows.GetFileAttributes(name)
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return os.ErrNotExist
		}
		if err != nil || attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errUnsafeStore
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	name, _ := windows.UTF16PtrFromString(abs)
	h, err := windows.CreateFile(name, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return errUnsafeStore
	}
	defer func() { _ = windows.CloseHandle(h) }()
	return validateStoreHandle(h, true)
}

func privateStoreDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := currentStoreUser()
	if err != nil {
		return nil, err
	}
	// Protected DACL: do not inherit broad grants from the user's chosen
	// config location. SYSTEM/admin remain trusted recovery principals.
	return windows.SecurityDescriptorFromString("O:" + user.String() + "D:P(A;OICI;FA;;;" + user.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
}

func createStoreDir(dir string) error {
	if err := privateStoreDir(dir); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(dir)
	if parent == dir {
		return errUnsafeStore
	}
	if err := createStoreDir(parent); err != nil {
		return err
	}
	sd, err := privateStoreDescriptor()
	if err != nil {
		return errUnsafeStore
	}
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return errUnsafeStore
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	err = windows.CreateDirectory(name, &sa)
	runtime.KeepAlive(sd)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return errUnsafeStore
	}
	return privateStoreDir(dir)
}

func createPrivateStoreTemp(dir string) (*os.File, error) {
	sd, err := privateStoreDescriptor()
	if err != nil {
		return nil, errUnsafeStore
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		return nil, errUnsafeStore
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	for range 10 {
		path := filepath.Join(dir, ".tokens-"+rand.Text())
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return nil, errUnsafeStore
		}
		h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL|windows.WRITE_DAC, 0, &sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		runtime.KeepAlive(sd)
		if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			continue
		}
		if err != nil {
			return nil, errUnsafeStore
		}
		f := os.NewFile(uintptr(h), path)
		// Apply and verify through the actual write handle before exposing it
		// to the caller. Creation is private too: there is no plaintext window.
		err = windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
		runtime.KeepAlive(sd)
		if err == nil {
			err = privateStoreFile(f)
		}
		if err != nil {
			_ = f.Close()
			_ = os.Remove(path)
			return nil, errUnsafeStore
		}
		return f, nil
	}
	return nil, errors.New("create private token store: temporary name collisions")
}
