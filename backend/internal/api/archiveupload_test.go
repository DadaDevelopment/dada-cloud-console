package api

import "testing"

func TestArchiveUploadIDFromCloneURL(t *testing.T) {
	cases := []struct {
		name     string
		cloneURL string
		wantID   string
	}{
		{
			name:     "zip",
			cloneURL: "s3://test-bucket/source-uploads/proj/app/12345678-abcd-uuid.zip",
			wantID:   "12345678",
		},
		{
			name:     "tar_gz",
			cloneURL: "s3://test-bucket/source-uploads/proj/app/abcdef01-2222.tar.gz",
			wantID:   "abcdef01",
		},
		{
			name:     "tgz",
			cloneURL: "s3://test-bucket/source-uploads/proj/app/abcdef02-3333.tgz",
			wantID:   "abcdef02",
		},
		{
			name:     "short_id_no_truncation",
			cloneURL: "s3://test-bucket/source-uploads/proj/app/abc.zip",
			wantID:   "abc",
		},
		{
			name:     "unknown_extension_falls_back_to_path_ext",
			cloneURL: "s3://test-bucket/source-uploads/proj/app/12345678-uuid.bin",
			wantID:   "12345678",
		},
		{
			name:     "not_s3_url",
			cloneURL: "https://example.com/source-uploads/proj/app/12345678.zip",
			wantID:   "",
		},
		{
			name:     "malformed_no_key",
			cloneURL: "s3://test-bucket",
			wantID:   "",
		},
		{
			name:     "empty",
			cloneURL: "",
			wantID:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := archiveUploadIDFromCloneURL(tc.cloneURL)
			if got != tc.wantID {
				t.Fatalf("archiveUploadIDFromCloneURL(%q) = %q, want %q", tc.cloneURL, got, tc.wantID)
			}
		})
	}
}

func TestSanitizeUploadedFilename(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain", input: "myapp.zip", want: "myapp.zip"},
		{name: "strips_unix_path", input: "/etc/passwd/myapp.zip", want: "myapp.zip"},
		{name: "strips_windows_path", input: `C:\Users\alex\myapp.zip`, want: "myapp.zip"},
		{name: "strips_control_chars", input: "my\x00app\x1b.zip", want: "myapp.zip"},
		{name: "trims_whitespace", input: "  myapp.zip  ", want: "myapp.zip"},
		{name: "empty", input: "", want: ""},
		{name: "dot_only", input: ".", want: ""},
		{name: "root_only", input: "/", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeUploadedFilename(tc.input)
			if got != tc.want {
				t.Fatalf("sanitizeUploadedFilename(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}

	long := ""
	for i := 0; i < uploadFilenameMaxLen+50; i++ {
		long += "a"
	}
	got := sanitizeUploadedFilename(long)
	if len(got) != uploadFilenameMaxLen {
		t.Fatalf("sanitizeUploadedFilename(long) length = %d, want %d", len(got), uploadFilenameMaxLen)
	}
}
