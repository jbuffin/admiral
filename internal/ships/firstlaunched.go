package ships

// firstLaunched is a ship's first launch stamp; a ship from before the stamp falls back to its latest launch.
func firstLaunched(o map[string]string) string {
	return cmpFirst(o["first-launched"], o["launched"])
}

func cmpFirst(first, launched string) string {
	if first != "" {
		return first
	}
	return launched
}
