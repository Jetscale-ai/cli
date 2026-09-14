//go:build windows

package auth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jetscale-ai/cli/internal/buildinfo"
	"golang.org/x/sys/windows"
)

func setTestStoreDACL(t *testing.T, path, sddl string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

func testStoreSDDL(t *testing.T) string {
	t.Helper()
	user, err := currentStoreUser()
	if err != nil {
		t.Fatal(err)
	}
	return "O:" + user.String() + "D:P(A;OICI;FA;;;" + user.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
}

func preparePrivateTestDir(t *testing.T, dir string) {
	t.Helper()
	// Only test-owned temporary directories are repermissioned.
	setTestStoreDACL(t, dir, testStoreSDDL(t))
}

func assertPrivateTokenFile(t *testing.T, path string) {
	t.Helper()
	f, err := openStoreFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := privateStoreFile(f); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsStoreACLPolicy(t *testing.T) {
	base := testStoreSDDL(t)
	for _, tt := range []struct {
		name, sddl string
		dir, safe  bool
	}{
		{"private-file", base, false, true},
		{"private-directory", base, true, true},
		{"system-owner", "O:SYD:P(A;;FA;;;SY)", false, true},
		{"admin-owner", "O:BAD:P(A;;FA;;;BA)", false, true},
		{"untrusted-owner", "O:WDD:P(A;;FA;;;SY)", false, false},
		{"null-dacl", "O:SYD:NO_ACCESS_CONTROL", false, false},
		{"missing-dacl", "O:SY", false, false},
		{"empty-dacl", "O:SYD:P", false, true},
		{"file-read", base + "(A;;FR;;;WD)", false, false},
		{"file-write", base + "(A;;FW;;;WD)", false, false},
		{"generic-file-read", base + "(A;;GR;;;WD)", false, false},
		{"file-delete", base + "(A;;SD;;;WD)", false, false},
		{"file-write-dacl", base + "(A;;WD;;;WD)", false, false},
		{"file-write-owner", base + "(A;;WO;;;WD)", false, false},
		{"directory-read", base + "(A;;FR;;;WD)", true, true},
		{"directory-generic-read", base + "(A;;GR;;;WD)", true, true},
		{"directory-write", base + "(A;;FW;;;WD)", true, false},
		{"directory-delete-child", base + "(A;;0x40;;;WD)", true, false},
		{"directory-delete", base + "(A;;SD;;;WD)", true, false},
		{"directory-write-owner", base + "(A;;WO;;;WD)", true, false},
		{"inherit-only", base + "(A;OICIIO;FA;;;WD)", true, true},
		{"deny-does-not-cancel-allow", base + "(D;;FR;;;WD)(A;;FR;;;WD)", false, false},
		{"unsupported-object-ace", base + "(OA;;FR;00000000-0000-0000-0000-000000000001;;WD)", false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString(tt.sddl)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateStoreACL(sd, tt.dir); (err == nil) != tt.safe {
				t.Fatalf("safe=%v, error=%v", tt.safe, err)
			}
		})
	}
}

func TestWindowsPrivateTempBeforeWrite(t *testing.T) {
	dir := privateTempDir(t)
	// Inherit-only grants may exist on a safe directory but must never leak
	// into the newly created credential file, even before its first write.
	setTestStoreDACL(t, dir, testStoreSDDL(t)+"(A;OIIO;FR;;;WD)")
	f, err := createPrivateStoreTemp(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := privateStoreFile(f); err != nil {
		t.Fatal(err)
	}
	info, err := f.Stat()
	if err != nil || info.Size() != 0 {
		t.Fatal("temporary file is not empty")
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("temporary file inherits permissions")
	}
	if _, err := f.WriteString("synthetic-token"); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsStoreRoundTripAndReplacement(t *testing.T) {
	isolateSessionStore(t)
	dir := filepath.Join(privateTempDir(t), "new", "config")
	t.Setenv(buildinfo.EnvVar("CONFIG_DIR"), dir)
	for _, value := range []string{"synthetic-first", "synthetic-replacement"} {
		if err := SetToken("fixture", TokenEntry{AccessToken: value}); err != nil {
			t.Fatal(err)
		}
		got, ok, err := GetToken("fixture")
		if err != nil || !ok || got.AccessToken != value {
			t.Fatal("token round trip failed")
		}
		p, _ := tokensPath()
		assertPrivateTokenFile(t, p)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 || entries[0].Name() != tokensLockName || entries[1].Name() != tokensFileName {
		t.Fatal("temporary files left behind")
	}
}

func TestWindowsHeldReaderAllowsReplacement(t *testing.T) {
	isolateSessionStore(t)
	t.Setenv(buildinfo.EnvVar("CONFIG_DIR"), filepath.Join(privateTempDir(t), "配置-🔒"))
	if err := SetToken("fixture", TokenEntry{AccessToken: "synthetic-old"}); err != nil {
		t.Fatal(err)
	}
	p, _ := tokensPath()
	reader, err := openStoreFile(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	if err := privateStoreFile(reader); err != nil {
		t.Fatal(err)
	}
	before, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	// Replacements must retain the temporary file's protected DACL rather than
	// inherit a directory's otherwise permissible inherit-only read grant.
	setTestStoreDACL(t, filepath.Dir(p), testStoreSDDL(t)+"(A;OIIO;FR;;;WD)")
	for _, value := range []string{"synthetic-new", "synthetic-newer"} {
		if err := SetToken("fixture", TokenEntry{AccessToken: value}); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		after, err := io.ReadAll(reader)
		if err != nil || string(before) != string(after) {
			t.Fatal("held reader did not retain its original snapshot")
		}
		got, ok, err := GetToken("fixture")
		if err != nil || !ok || got.AccessToken != value {
			t.Fatal("replacement not visible to new readers")
		}
		assertPrivateTokenFile(t, p)
		if err := privateStoreFile(reader); err != nil {
			t.Fatal("held reader lost its private ACL")
		}
	}
	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil || len(entries) != 2 || entries[0].Name() != tokensLockName || entries[1].Name() != tokensFileName {
		t.Fatal("replacement left temporary files behind")
	}
}

func TestWindowsReplacementFailurePreservesStore(t *testing.T) {
	isolateSessionStore(t)
	if err := SetToken("fixture", TokenEntry{AccessToken: "synthetic-old"}); err != nil {
		t.Fatal(err)
	}
	p, _ := tokensPath()
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		t.Fatal(err)
	}
	attrs, err := windows.GetFileAttributes(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetFileAttributes(name, attrs|windows.FILE_ATTRIBUTE_READONLY); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windows.SetFileAttributes(name, attrs); err != nil {
			t.Error(err)
		}
	})
	// POSIX replacement must not silently ignore the read-only attribute or
	// delete the old file as a fallback. No retry can repair this condition.
	err = SetToken("fixture", TokenEntry{AccessToken: "synthetic-new"})
	if err == nil || !strings.HasPrefix(err.Error(), "replace token store failed (OS error ") {
		t.Fatal("replacement failure did not report a sanitized OS error code")
	}
	if strings.Contains(err.Error(), filepath.Dir(p)) || strings.Contains(err.Error(), "synthetic-") {
		t.Fatal("replacement error exposed a path or credential")
	}
	got, ok, err := GetToken("fixture")
	if err != nil || !ok || got.AccessToken != "synthetic-old" {
		t.Fatal("failed replacement damaged the existing store")
	}
	assertPrivateTokenFile(t, p)
	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil || len(entries) != 2 || entries[0].Name() != tokensLockName || entries[1].Name() != tokensFileName {
		t.Fatal("failed replacement left temporary files behind")
	}
}

func TestWindowsStoreRejectsUnsafeLockACL(t *testing.T) {
	isolateSessionStore(t)
	if err := SaveTokens(TokenStore{}); err != nil {
		t.Fatal(err)
	}
	p, _ := tokensPath()
	path := filepath.Join(filepath.Dir(p), tokensLockName)
	setTestStoreDACL(t, path, testStoreSDDL(t)+"(A;;FA;;;WD)")
	if err := SetToken("fixture", TokenEntry{AccessToken: "synthetic"}); err == nil {
		t.Fatal("unsafe lock accepted for update")
	}
	if err := SaveTokens(TokenStore{}); err == nil {
		t.Fatal("unsafe lock accepted for replacement")
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || validateStoreACL(sd, false) == nil {
		t.Fatal("existing unsafe lock ACL was repaired")
	}
}

func TestWindowsStoreRejectsPermissiveACLs(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "directory"}[directory], func(t *testing.T) {
			isolateSessionStore(t)
			if err := SaveTokens(TokenStore{}); err != nil {
				t.Fatal(err)
			}
			p, _ := tokensPath()
			if directory {
				p = filepath.Dir(p)
			}
			sddl := testStoreSDDL(t) + "(A;;FA;;;WD)"
			setTestStoreDACL(t, p, sddl)
			if _, err := LoadTokens(); err == nil {
				t.Fatal("permissive ACL accepted on load")
			}
			if err := SaveTokens(TokenStore{}); err == nil {
				t.Fatal("permissive ACL accepted on save")
			}
			sd, err := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
			if err != nil || validateStoreACL(sd, directory) == nil {
				t.Fatal("existing ACL was unexpectedly repaired")
			}
		})
	}
}

func TestWindowsSessionLoginSaveReload(t *testing.T) {
	isolateSessionStore(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/sign-in":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-cookie", Path: "/", HttpOnly: true})
			_, _ = io.WriteString(w, `{}`)
		case "/api/v2/auth/me":
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != "synthetic-cookie" {
				t.Error("missing synthetic session")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, fakeWhoami)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s, err := LoginSession(srv.URL, "fixture", "synthetic-password", "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.HTTPClient.CloseIdleConnections()
	if err := s.Save("fixture"); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenSession("fixture", srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	defer restored.HTTPClient.CloseIdleConnections()
	if _, err := restored.Whoami(); err != nil {
		t.Fatal(err)
	}
}
