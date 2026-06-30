package main

func hasFlag(args cliArgs, name string) bool {
	if _, ok := args.Flags[name]; ok {
		return true
	}
	if _, ok := args.Bools[name]; ok {
		return true
	}
	return false
}
