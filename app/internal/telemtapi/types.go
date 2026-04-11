package telemtapi

import (
	"encoding/json"
	"sort"
	"strings"
)

type HealthParseClass string

const (
	HealthParseClassComplete   HealthParseClass = "complete"
	HealthParseClassIncomplete HealthParseClass = "incomplete"
)

type HealthEnvelope struct {
	OK       *bool       `json:"ok"`
	Data     *HealthData `json:"data"`
	Revision string      `json:"revision,omitempty"`
}

type HealthData struct {
	Status   string `json:"status"`
	ReadOnly *bool  `json:"read_only"`
}

func (h HealthEnvelope) ParseClass() HealthParseClass {
	if h.DegradedReason() == "" {
		return HealthParseClassComplete
	}
	return HealthParseClassIncomplete
}

func (h HealthEnvelope) DegradedReason() string {
	if h.OK == nil {
		return "missing_ok"
	}
	if h.Data == nil {
		return "missing_data"
	}
	if strings.TrimSpace(h.Data.Status) == "" {
		return "missing_status"
	}
	if h.Data.ReadOnly == nil {
		return "missing_read_only"
	}
	return ""
}

func (h HealthEnvelope) IsHealthy() bool {
	if h.ParseClass() != HealthParseClassComplete {
		return false
	}
	return *h.OK && strings.EqualFold(strings.TrimSpace(h.Data.Status), "ok")
}

type UsersEnvelope struct {
	OK       *bool           `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Users    json.RawMessage `json:"users"`
	Revision string          `json:"revision,omitempty"`

	rawPayload json.RawMessage
}

func (u *UsersEnvelope) UnmarshalJSON(raw []byte) error {
	u.OK = nil
	u.Data = nil
	u.Users = nil
	u.Revision = ""
	u.rawPayload = cloneRawMessage(raw)

	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if !strings.HasPrefix(trimmed, "{") {
		return nil
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}

	if rawOK, found := object["ok"]; found {
		var okValue bool
		if err := json.Unmarshal(rawOK, &okValue); err == nil {
			u.OK = &okValue
		}
	}

	if rawData, found := object["data"]; found {
		u.Data = cloneRawMessage(rawData)
	}
	if rawUsers, found := object["users"]; found {
		u.Users = cloneRawMessage(rawUsers)
	}
	if rawRevision, found := object["revision"]; found {
		var revision string
		if err := json.Unmarshal(rawRevision, &revision); err == nil {
			u.Revision = revision
		}
	}

	return nil
}

type UsersParseClass string

const (
	UsersParseClassUsableLink          UsersParseClass = "usable_tls_link"
	UsersParseClassNoUsers             UsersParseClass = "no_users"
	UsersParseClassNoTLSLinks          UsersParseClass = "no_tls_links"
	UsersParseClassIncompleteStructure UsersParseClass = "incomplete_payload_shape"
)

type UserProjection struct {
	Name     string
	TLSLinks []string
}

type LinkSelection struct {
	Class          UsersParseClass
	DegradedReason string
	SelectedLink   string
	UsersCount     int
	CandidateCount int
}

func (s LinkSelection) HasUsableLink() bool {
	return s.Class == UsersParseClassUsableLink && strings.TrimSpace(s.SelectedLink) != ""
}

func (s LinkSelection) RedactedSelectedLink() string {
	if !s.HasUsableLink() {
		return ""
	}
	return "[redacted-proxy-link]"
}

func (u UsersEnvelope) SelectStartupLink() LinkSelection {
	users, usersFound, reason := u.projectUsers()
	if !usersFound {
		if strings.TrimSpace(reason) == "" {
			reason = "users_collection_missing_or_unsupported"
		}
		return LinkSelection{
			Class:          UsersParseClassIncompleteStructure,
			DegradedReason: reason,
		}
	}

	if len(users) == 0 {
		return LinkSelection{
			Class:          UsersParseClassNoUsers,
			DegradedReason: "users_collection_empty",
			UsersCount:     0,
		}
	}

	selected := ""
	candidateCount := 0
	for _, user := range users {
		for _, candidate := range user.TLSLinks {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				continue
			}
			candidateCount++
			if selected == "" && IsUsableProxyLink(candidate) {
				selected = candidate
			}
		}
	}

	if selected != "" {
		return LinkSelection{
			Class:          UsersParseClassUsableLink,
			SelectedLink:   selected,
			UsersCount:     len(users),
			CandidateCount: candidateCount,
		}
	}

	reason := "users_without_tls_links"
	if candidateCount > 0 {
		reason = "tls_links_present_but_unusable"
	}

	return LinkSelection{
		Class:          UsersParseClassNoTLSLinks,
		DegradedReason: reason,
		UsersCount:     len(users),
		CandidateCount: candidateCount,
	}
}

func IsUsableProxyLink(value string) bool {
	link := strings.ToLower(strings.TrimSpace(value))
	if link == "" {
		return false
	}
	return strings.HasPrefix(link, "tg://proxy?") || strings.HasPrefix(link, "https://t.me/proxy?")
}

func (u UsersEnvelope) projectUsers() ([]UserProjection, bool, string) {
	rootObject, ok := parseJSONObject(u.rawPayload)
	if !ok {
		return nil, false, "payload_not_object"
	}

	if looksLikeUsersEnvelope(rootObject) {
		if u.OK == nil || !*u.OK {
			return nil, false, "response_not_ok"
		}

		collectionRaw, found := extractUsersCollectionFromEnvelope(u.Users, u.Data)
		if !found {
			return nil, false, "users_collection_missing_or_unsupported"
		}

		users, parsed := parseUsersCollection(collectionRaw)
		if !parsed {
			return nil, false, "users_collection_missing_or_unsupported"
		}

		return users, true, ""
	}

	users, parsed := parseUsersMapCollection(rootObject)
	if !parsed {
		return nil, false, "users_collection_missing_or_unsupported"
	}

	return users, true, ""
}

func parseUsersCollection(raw json.RawMessage) ([]UserProjection, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, false
	}
	if trimmed == "null" {
		return []UserProjection{}, true
	}

	if strings.HasPrefix(trimmed, "[") {
		var rawArray []json.RawMessage
		if err := json.Unmarshal(raw, &rawArray); err != nil {
			return nil, false
		}
		users := make([]UserProjection, 0, len(rawArray))
		for _, entry := range rawArray {
			user, ok := parseUserProjection(entry, "")
			if !ok {
				return nil, false
			}
			users = append(users, user)
		}
		return users, true
	}

	if strings.HasPrefix(trimmed, "{") {
		var rawObject map[string]json.RawMessage
		if err := json.Unmarshal(raw, &rawObject); err != nil {
			return nil, false
		}
		return parseUsersMapCollection(rawObject)
	}

	return nil, false
}

func parseUsersMapCollection(rawObject map[string]json.RawMessage) ([]UserProjection, bool) {
	if len(rawObject) == 0 {
		return []UserProjection{}, true
	}

	keys := make([]string, 0, len(rawObject))
	for key := range rawObject {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	users := make([]UserProjection, 0, len(keys))
	for _, key := range keys {
		user, ok := parseUserProjection(rawObject[key], key)
		if !ok {
			return nil, false
		}
		users = append(users, user)
	}

	return users, true
}

func looksLikeUsersEnvelope(rawObject map[string]json.RawMessage) bool {
	if rawOK, hasOK := rawObject["ok"]; hasOK && looksLikeEnvelopeScalar(rawOK) {
		return true
	}
	if rawRevision, hasRevision := rawObject["revision"]; hasRevision && looksLikeEnvelopeScalar(rawRevision) {
		return true
	}
	if rawUsers, hasUsers := rawObject["users"]; hasUsers && looksLikeEnvelopeUsersCollection(rawUsers) {
		return true
	}
	if rawData, hasData := rawObject["data"]; hasData && looksLikeEnvelopeDataCollection(rawData) {
		return true
	}

	return false
}

func looksLikeEnvelopeScalar(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return false
	}

	return !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[")
}

func looksLikeEnvelopeUsersCollection(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "[") {
		return true
	}
	if !strings.HasPrefix(trimmed, "{") {
		return true
	}

	usersObject, ok := parseJSONObject(raw)
	if !ok {
		return false
	}
	if len(usersObject) == 0 {
		return true
	}
	if looksLikeSupportedUserObject(usersObject) {
		return false
	}

	return true
}

func looksLikeEnvelopeDataCollection(raw json.RawMessage) bool {
	dataObject, ok := parseJSONObject(raw)
	if !ok {
		return false
	}

	usersCollection, found := dataObject["users"]
	if !found {
		return false
	}

	return looksLikeEnvelopeUsersCollection(usersCollection)
}

func extractUsersCollectionFromEnvelope(usersRaw json.RawMessage, dataRaw json.RawMessage) (json.RawMessage, bool) {
	if len(usersRaw) > 0 {
		return usersRaw, true
	}

	dataObject, ok := parseJSONObject(dataRaw)
	if !ok {
		return nil, false
	}

	usersFromData, found := dataObject["users"]
	if !found {
		return nil, false
	}

	return usersFromData, true
}

func parseUserProjection(raw json.RawMessage, fallbackName string) (UserProjection, bool) {
	userObject, ok := parseJSONObject(raw)
	if !ok {
		return UserProjection{}, false
	}
	if !looksLikeSupportedUserObject(userObject) {
		return UserProjection{}, false
	}

	tlsLinks, ok := parseTLSLinks(userObject["tls"])
	if !ok {
		return UserProjection{}, false
	}

	name, ok := parseUserName(userObject, fallbackName)
	if !ok {
		return UserProjection{}, false
	}

	return UserProjection{
		Name:     name,
		TLSLinks: tlsLinks,
	}, true
}

func looksLikeSupportedUserObject(userObject map[string]json.RawMessage) bool {
	if _, ok := userObject["tls"]; ok {
		return true
	}
	if _, ok := userObject["username"]; ok {
		return true
	}
	if _, ok := userObject["name"]; ok {
		return true
	}
	if _, ok := userObject["user"]; ok {
		return true
	}
	return false
}

func parseUserName(userObject map[string]json.RawMessage, fallbackName string) (string, bool) {
	for _, field := range []string{"username", "name", "user"} {
		name, present, valid := parseOptionalStringJSONValue(userObject[field])
		if !present {
			continue
		}
		if !valid {
			return "", false
		}
		if name != "" {
			return name, true
		}
	}

	return strings.TrimSpace(fallbackName), true
}

func parseTLSLinks(raw json.RawMessage) ([]string, bool) {
	if len(raw) == 0 {
		return nil, true
	}

	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, true
	}

	var rawArray []json.RawMessage
	if err := json.Unmarshal(raw, &rawArray); err != nil {
		return nil, false
	}

	links := make([]string, 0, len(rawArray))
	for _, entry := range rawArray {
		var link string
		if err := json.Unmarshal(entry, &link); err != nil {
			return nil, false
		}
		links = append(links, link)
	}

	return dedupeNonEmpty(links), true
}

func parseJSONObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, false
	}
	if !strings.HasPrefix(trimmed, "{") {
		return nil, false
	}

	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, false
	}
	return parsed, true
}

func parseOptionalStringJSONValue(raw json.RawMessage) (string, bool, bool) {
	if len(raw) == 0 {
		return "", false, true
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, false
	}

	return strings.TrimSpace(value), true, true
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}

	clone := make([]byte, len(raw))
	copy(clone, raw)
	return clone
}

func dedupeNonEmpty(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}
