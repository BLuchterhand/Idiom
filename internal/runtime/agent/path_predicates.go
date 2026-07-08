package agent

func looksLikeWindowsAbsolutePath(path string) bool {
	return len(path) >= 3 &&
		((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) &&
		path[1] == ':' &&
		(path[2] == '/' || path[2] == '\\')
}
