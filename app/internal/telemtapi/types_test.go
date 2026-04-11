package telemtapi

import (
	"encoding/json"
	"testing"
)

func TestUsersEnvelopeSelectStartupLinkVariants(t *testing.T) {
	t.Parallel()

	const usableLink = "tg://proxy?server=127.0.0.1&port=443&secret=abcdef"

	testCases := []struct {
		name           string
		payload        string
		wantClass      UsersParseClass
		wantReason     string
		wantUsers      int
		wantCandidates int
		wantUsable     bool
	}{
		{
			name:           "legacy top-level map users payload remains supported",
			payload:        `{"main":{"tls":["` + usableLink + `"]}}`,
			wantClass:      UsersParseClassUsableLink,
			wantUsers:      1,
			wantCandidates: 1,
			wantUsable:     true,
		},
		{
			name: "legacy top-level map with reserved usernames remains supported",
			payload: `{
				"ok":{"tls":["` + usableLink + `"]},
				"data":{"tls":["` + usableLink + `"]},
				"users":{"tls":["` + usableLink + `"]},
				"revision":{"tls":["` + usableLink + `"]}
			}`,
			wantClass:      UsersParseClassUsableLink,
			wantUsers:      4,
			wantCandidates: 4,
			wantUsable:     true,
		},
		{
			name:           "wrapper users map requires ok true and remains supported",
			payload:        `{"ok":true,"users":{"main":{"tls":["` + usableLink + `"]}}}`,
			wantClass:      UsersParseClassUsableLink,
			wantUsers:      1,
			wantCandidates: 1,
			wantUsable:     true,
		},
		{
			name:           "wrapper data users map remains supported",
			payload:        `{"ok":true,"data":{"users":{"main":{"tls":["` + usableLink + `"]}}}}`,
			wantClass:      UsersParseClassUsableLink,
			wantUsers:      1,
			wantCandidates: 1,
			wantUsable:     true,
		},
		{
			name:       "wrapper with ok false is incomplete payload shape",
			payload:    `{"ok":false,"users":{"main":{"tls":["` + usableLink + `"]}}}`,
			wantClass:  UsersParseClassIncompleteStructure,
			wantReason: "response_not_ok",
		},
		{
			name:       "wrapper with missing ok is incomplete payload shape",
			payload:    `{"users":{"main":{"tls":["` + usableLink + `"]}}}`,
			wantClass:  UsersParseClassIncompleteStructure,
			wantReason: "response_not_ok",
		},
		{
			name:       "wrapper with non-boolean ok is incomplete payload shape",
			payload:    `{"ok":"true","users":{"main":{"tls":["` + usableLink + `"]}}}`,
			wantClass:  UsersParseClassIncompleteStructure,
			wantReason: "response_not_ok",
		},
		{
			name:       "wrapper with primitive users array is incomplete payload shape",
			payload:    `{"ok":true,"users":["` + usableLink + `"]}`,
			wantClass:  UsersParseClassIncompleteStructure,
			wantReason: "users_collection_missing_or_unsupported",
		},
		{
			name:       "top-level array payload is incomplete payload shape",
			payload:    `[{"tls":["` + usableLink + `"]}]`,
			wantClass:  UsersParseClassIncompleteStructure,
			wantReason: "payload_not_object",
		},
		{
			name:       "nested tls schema drift is classified as incomplete payload shape",
			payload:    `{"ok":true,"users":{"main":{"profile":{"tls":["` + usableLink + `"]}}}}`,
			wantClass:  UsersParseClassIncompleteStructure,
			wantReason: "users_collection_missing_or_unsupported",
		},
		{
			name:       "recognized user without tls is classified as no tls links",
			payload:    `{"ok":true,"users":{"main":{"username":"main"}}}`,
			wantClass:  UsersParseClassNoTLSLinks,
			wantReason: "users_without_tls_links",
			wantUsers:  1,
		},
		{
			name:       "user with non-string username is incomplete payload shape",
			payload:    `{"ok":true,"users":{"main":{"username":123}}}`,
			wantClass:  UsersParseClassIncompleteStructure,
			wantReason: "users_collection_missing_or_unsupported",
		},
		{
			name:       "user with non-string name is incomplete payload shape",
			payload:    `{"ok":true,"users":{"main":{"name":{"first":"main"}}}}`,
			wantClass:  UsersParseClassIncompleteStructure,
			wantReason: "users_collection_missing_or_unsupported",
		},
		{
			name:       "user with non-string user field is incomplete payload shape",
			payload:    `{"ok":true,"users":{"main":{"user":true}}}`,
			wantClass:  UsersParseClassIncompleteStructure,
			wantReason: "users_collection_missing_or_unsupported",
		},
		{
			name:       "empty users collection is classified explicitly",
			payload:    `{"ok":true,"users":{}}`,
			wantClass:  UsersParseClassNoUsers,
			wantReason: "users_collection_empty",
		},
		{
			name:       "malformed tls field type is incomplete payload shape",
			payload:    `{"ok":true,"users":{"main":{"tls":{"link":"` + usableLink + `"}}}}`,
			wantClass:  UsersParseClassIncompleteStructure,
			wantReason: "users_collection_missing_or_unsupported",
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			envelope := mustUnmarshalUsersEnvelope(t, testCase.payload)
			selection := envelope.SelectStartupLink()

			if selection.Class != testCase.wantClass {
				t.Fatalf("unexpected class: got %q, want %q", selection.Class, testCase.wantClass)
			}

			if selection.DegradedReason != testCase.wantReason {
				t.Fatalf("unexpected degraded reason: got %q, want %q", selection.DegradedReason, testCase.wantReason)
			}

			if selection.UsersCount != testCase.wantUsers {
				t.Fatalf("unexpected users count: got %d, want %d", selection.UsersCount, testCase.wantUsers)
			}

			if selection.CandidateCount != testCase.wantCandidates {
				t.Fatalf("unexpected candidate count: got %d, want %d", selection.CandidateCount, testCase.wantCandidates)
			}

			if selection.HasUsableLink() != testCase.wantUsable {
				t.Fatalf("unexpected usable-link flag: got %t, want %t", selection.HasUsableLink(), testCase.wantUsable)
			}

			if testCase.wantUsable {
				if selection.SelectedLink == "" {
					t.Fatalf("expected selected link for usable result")
				}
				if redacted := selection.RedactedSelectedLink(); redacted != "[redacted-proxy-link]" {
					t.Fatalf("expected redacted selected link marker, got %q", redacted)
				}
			}
		})
	}
}

func mustUnmarshalUsersEnvelope(t *testing.T, raw string) UsersEnvelope {
	t.Helper()

	var envelope UsersEnvelope
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("failed to unmarshal UsersEnvelope payload: %v", err)
	}

	return envelope
}
