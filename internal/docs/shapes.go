package docs

// The three shapes below exist because a handful of endpoints answer with something that
// has no Go type in this repository, and the alternative to naming them was a fake type or
// no contract at all (doc/310 §8.2.1). Each one is used by an enumerated list in
// cmd/apicontract (`declaredContracts`), so adding a fourth is a reviewable one-line diff
// rather than a new habit.

// UpstreamPassthrough is the body of the completions endpoints that relay an
// OpenAI-compatible answer from the upstream engine without re-encoding it.
//
// The document can therefore promise "a JSON object" and no more: inventing field names
// here would be a contract this service does not enforce.
type UpstreamPassthrough map[string]interface{}

// CallbackAck is the acknowledgement the IM adapters (.im/feishu, .im/dingtalk) return to
// the platform: `{"code":0}` marshalled by the handler and written with c.Data, which is
// why no Go struct is emitted at runtime.
type CallbackAck struct {
	Code int `json:"code" example:"0"`
}

// FreeFormObject is the setup manager's configuration snapshot: a nested map whose keys
// depend on which sections the deployment configured. It is documented as free-form
// because that is the truth; the alternative was a struct the engine does not have.
type FreeFormObject map[string]interface{}
