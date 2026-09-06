package sanitize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactSecretData(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "root level data block",
			input: `apiVersion: v1
kind: Secret
metadata:
  name: my-secret
data:
  username: dXNlcm5hbWU=
  password: cGFzc3dvcmQ=
type: Opaque`,
			want: `apiVersion: v1
kind: Secret
metadata:
  name: my-secret
data:
  username: [REDACTED]
  password: [REDACTED]
type: Opaque`,
		},
		{
			name: "indented data block (list item)",
			input: `items:
- apiVersion: v1
  kind: Secret
  metadata:
    name: my-secret
  data:
    token: c2VjcmV0
  type: Opaque`,
			want: `items:
- apiVersion: v1
  kind: Secret
  metadata:
    name: my-secret
  data:
    token: [REDACTED]
  type: Opaque`,
		},
		{
			name: "deeply indented data block",
			input: `spec:
  template:
    data:
      key1: value1
      key2: value2
    other: field`,
			want: `spec:
  template:
    data:
      key1: [REDACTED]
      key2: [REDACTED]
    other: field`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactSecretData(tt.input)
			if got != tt.want {
				t.Errorf("redactSecretData():\ngot:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestSanitizeStructuredFile(t *testing.T) {
	dir := t.TempDir()

	t.Run("secret data redacted", func(t *testing.T) {
		f := filepath.Join(dir, "secret.yaml")
		content := "apiVersion: v1\nkind: Secret\ndata:\n  password: dXNlcm5hbWU=\n"
		writeTestFile(t, f, content)

		var res Result
		sanitizeStructured(f, &res)

		got, _ := os.ReadFile(f)
		if !strings.Contains(string(got), "[REDACTED]") {
			t.Errorf("expected secret data to be redacted, got: %s", got)
		}
		if res.ItemsSanitized == 0 {
			t.Error("expected items sanitized > 0")
		}
	})

	t.Run("base64 in non-secret", func(t *testing.T) {
		f := filepath.Join(dir, "configmap.yaml")
		content := "apiVersion: v1\nkind: ConfigMap\ndata:\n  key: QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVoxMjM0NTY3ODk=\n"
		writeTestFile(t, f, content)

		var res Result
		sanitizeStructured(f, &res)

		got, _ := os.ReadFile(f)
		if !strings.Contains(string(got), "[REDACTED - Base64 data removed]") {
			t.Errorf("expected base64 data to be redacted, got: %s", got)
		}
	})

	t.Run("JWT token", func(t *testing.T) {
		f := filepath.Join(dir, "deploy.yaml")
		content := "token: eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.abc123\n"
		writeTestFile(t, f, content)

		var res Result
		sanitizeStructured(f, &res)

		got, _ := os.ReadFile(f)
		if !strings.Contains(string(got), "[REDACTED-JWT-TOKEN]") {
			t.Errorf("expected JWT to be redacted, got: %s", got)
		}
	})

	t.Run("bearer token", func(t *testing.T) {
		f := filepath.Join(dir, "auth.yaml")
		content := "header: Bearer abcdefghijklmnopqrstuvwxyz1234567890\n"
		writeTestFile(t, f, content)

		var res Result
		sanitizeStructured(f, &res)

		got, _ := os.ReadFile(f)
		if !strings.Contains(string(got), "[REDACTED-BEARER-TOKEN]") {
			t.Errorf("expected bearer token to be redacted, got: %s", got)
		}
	})

	t.Run("SSH private key", func(t *testing.T) {
		f := filepath.Join(dir, "ssh.yaml")
		content := "key: |\n  -----BEGIN RSA PRIVATE KEY-----\n  MIIBogIBAAJ...\n  -----END RSA PRIVATE KEY-----\n"
		writeTestFile(t, f, content)

		var res Result
		sanitizeStructured(f, &res)

		got, _ := os.ReadFile(f)
		if !strings.Contains(string(got), "[REDACTED - SSH Private Key Removed]") {
			t.Errorf("expected SSH key to be redacted, got: %s", got)
		}
	})
}

func TestSanitizeTextFile(t *testing.T) {
	dir := t.TempDir()

	t.Run("JWT in log", func(t *testing.T) {
		f := filepath.Join(dir, "app.log")
		content := "auth token: eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.abc123 received\n"
		writeTestFile(t, f, content)

		var res Result
		sanitizeText(f, &res)

		got, _ := os.ReadFile(f)
		if !strings.Contains(string(got), "[REDACTED-JWT-TOKEN]") {
			t.Errorf("expected JWT to be redacted, got: %s", got)
		}
	})

	t.Run("authorization header", func(t *testing.T) {
		f := filepath.Join(dir, "access.log")
		content := "Authorization: Bearer abcdefghijklmnopqrstuvwxyz1234567890\n"
		writeTestFile(t, f, content)

		var res Result
		sanitizeText(f, &res)

		got, _ := os.ReadFile(f)
		if !strings.Contains(string(got), "[REDACTED-TOKEN]") {
			t.Errorf("expected auth header to be redacted, got: %s", got)
		}
	})

	t.Run("password parameter", func(t *testing.T) {
		f := filepath.Join(dir, "conn.txt")
		content := "postgresql://user:password=mysecretpass&host=localhost\n"
		writeTestFile(t, f, content)

		var res Result
		sanitizeText(f, &res)

		got, _ := os.ReadFile(f)
		if !strings.Contains(string(got), "password=[REDACTED]") {
			t.Errorf("expected password to be redacted, got: %s", got)
		}
	})
}

func TestRunFullDirectory(t *testing.T) {
	dir := t.TempDir()

	writeTestFile(t, filepath.Join(dir, "secret.yaml"),
		"kind: Secret\ndata:\n  pw: c2VjcmV0\n")
	writeTestFile(t, filepath.Join(dir, "app.log"),
		"token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.sig end\n")
	writeTestFile(t, filepath.Join(dir, "readme.md"),
		"This should not be processed\n")

	res := Run(dir, nil)

	if res.FilesProcessed != 2 {
		t.Errorf("expected 2 files processed, got %d", res.FilesProcessed)
	}
	if res.ItemsSanitized < 2 {
		t.Errorf("expected at least 2 items sanitized, got %d", res.ItemsSanitized)
	}

	report, err := os.ReadFile(filepath.Join(dir, "sanitization-report.txt"))
	if err != nil {
		t.Fatalf("expected report file to exist: %v", err)
	}
	if !strings.Contains(string(report), "SANITIZATION SUMMARY") {
		t.Error("report missing summary section")
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
