// Copyright (c) 2026 Visvasity LLC

package servers

import "fmt"

// ValidName reports whether name is a valid service / relay label: 1..63 bytes of
// lowercase a-z, 0-9 and '-', not starting or ending with '-'.
func ValidName(name string) error {
	if name == "" {
		return fmt.Errorf("name must not be empty")
	}
	if len(name) > 63 {
		return fmt.Errorf("name %q exceeds the 63-character label limit", name)
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			// alphanumerics valid anywhere
		case c == '-':
			if i == 0 || i == len(name)-1 {
				return fmt.Errorf("name %q must not start or end with a hyphen", name)
			}
		default:
			return fmt.Errorf("name %q contains invalid character %q (allowed: a-z, 0-9, '-')", name, c)
		}
	}
	return nil
}
