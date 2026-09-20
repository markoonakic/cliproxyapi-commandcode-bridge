// Package identity holds the plugin's provider key and display metadata.
//
// The provider key is the single source of truth. It is set from the credential
// file's "type" field, so it must match the "type" written into enrolled
// credentials and the compiled shared library's file name. Keeping it here
// avoids the key drifting between capabilities.
package identity

// ProviderKey is the Command Code provider key.
//
// This is intentionally distinct from the community plugin's "commandcode-bridge"
// key, so both plugins can run at once during the side-by-side trial: the
// community plugin keeps its key and credential, and this plugin owns
// "command-code" with its own credential file.
const ProviderKey = "command-code"

// DisplayName is the user-facing label shown by the Management Center.
const DisplayName = "Command Code"

// Metadata identity for the plugin registration.
const (
	Name = "Command Code"
	// Repository is the published source for the plugin binary.
	Repository = "https://github.com/markoonakic/cliproxyapi-commandcode-bridge"
	// Author identifies the publisher in the plugin registry listing.
	Author = "Command Code"
)

// UserAgent identifies this plugin on upstream Command Code requests.
const UserAgent = "cliproxyapi-command-code"

// CredentialType is the value written into a credential file's "type" field.
// It must equal ProviderKey, because the host derives the provider key from it.
const CredentialType = ProviderKey

// FileNamePrefix names credential files for this provider.
const FileNamePrefix = ProviderKey
