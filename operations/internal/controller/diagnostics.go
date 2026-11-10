package controller

func validDiagnosticCode(code string) bool {
	if code == "" || len(code) > 96 {
		return false
	}
	for _, c := range code {
		if c != '_' && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}
