package directory

import (
	"fmt"
	"strconv"
	"strings"
)

// PolicyKind says what values a password-policy setting takes.
type PolicyKind int

// The two shapes a setting has.
const (
	// PolicyOnOff takes on, off or default.
	PolicyOnOff PolicyKind = iota
	// PolicyNumber takes a non-negative integer or default.
	PolicyNumber
)

// PolicyField describes one setting of `samba-tool domain passwordsettings`.
// Name is the samba-tool flag without its leading dashes — the dashes are
// added at the one place the argv is built, so a name can never read as some
// other flag. Label is the exact line `show` prints it under, which is how the
// parser and the editor stay pointed at the same thing.
type PolicyField struct {
	Name  string
	Label string
	Kind  PolicyKind
	// Help is what the edit prompt says about the value.
	Help string
}

// PolicyFields is the settings table, in the order `show` prints them.
var PolicyFields = []PolicyField{
	{Name: "complexity", Label: "Password complexity", Kind: PolicyOnOff,
		Help: "on, off or default (on). Complexity requires three character classes."},
	{Name: "store-plaintext", Label: "Store plaintext passwords", Kind: PolicyOnOff,
		Help: "on, off or default (off). Storing plaintext is for MIT/Heimdal interop only."},
	{Name: "history-length", Label: "Password history length", Kind: PolicyNumber,
		Help: "How many old passwords are remembered, or default (24). 0 disables."},
	{Name: "min-pwd-length", Label: "Minimum password length", Kind: PolicyNumber,
		Help: "A number of characters, or default (7)."},
	{Name: "min-pwd-age", Label: "Minimum password age (days)", Kind: PolicyNumber,
		Help: "Days before a password may change again, or default (1)."},
	{Name: "max-pwd-age", Label: "Maximum password age (days)", Kind: PolicyNumber,
		Help: "Days before a password must change, 0 for never, or default (43)."},
	{Name: "account-lockout-duration", Label: "Account lockout duration (mins)",
		Kind: PolicyNumber,
		Help: "Minutes a locked account stays locked, or default (30)."},
	{Name: "account-lockout-threshold", Label: "Account lockout threshold (attempts)",
		Kind: PolicyNumber,
		Help: "Failed attempts before lockout, 0 for never, or default (0)."},
	{Name: "reset-account-lockout-after", Label: "Reset account lockout after (mins)",
		Kind: PolicyNumber,
		Help: "Minutes before the failed-attempt counter resets, or default (30)."},
}

// PolicyFieldByName returns the field a flag name addresses.
func PolicyFieldByName(name string) (PolicyField, bool) {
	for _, f := range PolicyFields {
		if f.Name == name {
			return f, true
		}
	}
	return PolicyField{}, false
}

// PolicyFieldByLabel returns the field a `show` line belongs to.
func PolicyFieldByLabel(label string) (PolicyField, bool) {
	for _, f := range PolicyFields {
		if strings.EqualFold(f.Label, label) {
			return f, true
		}
	}
	return PolicyField{}, false
}

// ValidatePolicyValue checks a value against what its setting takes, so a typo
// is refused at the prompt with the rule named, rather than sent to samba-tool
// to be refused with a usage string.
func ValidatePolicyValue(field PolicyField, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("a value is required — %s", strings.ToLower(field.Help))
	}
	if value == "default" {
		return nil
	}
	switch field.Kind {
	case PolicyOnOff:
		if value != "on" && value != "off" {
			return fmt.Errorf("%s takes on, off or default, not %q", field.Label, value)
		}
	case PolicyNumber:
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("%s takes a non-negative number or default, not %q",
				field.Label, value)
		}
	}
	return nil
}

// PolicySetting is one line of `samba-tool domain passwordsettings show`,
// parsed: the setting, its current value, and the field that can change it.
type PolicySetting struct {
	Label string `json:"label"`
	Value string `json:"value"`
	// Name is the samba-tool flag (without dashes) that sets this line, empty
	// for a line the tool cannot edit.
	Name string `json:"name,omitempty"`
}

// PasswordPolicy is the whole of `passwordsettings show`.
type PasswordPolicy struct {
	Settings []PolicySetting `json:"settings,omitempty"`
	// Read records that the command ran and was parsed; an unreadable policy
	// and an empty one are different things.
	Read bool `json:"read"`
}
