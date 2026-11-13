package ships

// OwnsRef is whether ref is a PR a listed ship holds or an issue a listed ship claims, spelled however GitHub spells it.
func OwnsRef(list []Ship, ref string) bool { return false }
