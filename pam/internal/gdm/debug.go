// TiCS: disabled // This is only built for tests.

//go:build pam_gdm_debug

package gdm

func init() {
	checkMembersFunc = checkMembersDebug
	validateJSONFunc = validateJSONDebug
	stringifyEventDataFunc = stringifyEventDataDebug
}
