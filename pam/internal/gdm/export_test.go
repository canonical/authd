package gdm

func init() {
	checkMembersFunc = checkMembersDebug
	validateJSONFunc = validateJSONDebug
	stringifyEventDataFunc = stringifyEventDataDebug
}

func SetDebuggingSafeEventDataFunc(toggle bool) {
	if toggle {
		stringifyEventDataFunc = stringifyEventDataDebug
		return
	}

	stringifyEventDataFunc = stringifyEventDataFiltered
}
