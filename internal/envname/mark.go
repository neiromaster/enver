package envname

import "strings"

// SecretMark is the leading key prefix marking a variable as a secret-encryption candidate; file syntax, stripped before the name is used.
const SecretMark = '~'

// SplitMarked strips at most one leading SecretMark from key and reports whether it had one.
func SplitMarked(key string) (name string, marked bool) {
	if strings.HasPrefix(key, string(SecretMark)) {
		return key[1:], true
	}
	return key, false
}

// JoinMarked reattaches SecretMark to name when marked; the inverse of SplitMarked.
func JoinMarked(name string, marked bool) string {
	if marked {
		return string(SecretMark) + name
	}
	return name
}

// IsMarked reports whether key carries a SecretMark on a non-empty name.
func IsMarked(key string) bool {
	name, marked := SplitMarked(key)
	return marked && name != ""
}
