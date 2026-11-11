package cli

const DefaultPort = "7420"

func serves(port, dir string) bool {
	_, ok := probe(port, dir)
	return ok
}
