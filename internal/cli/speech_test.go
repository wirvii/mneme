package cli

import "testing"

func TestSpeechCommandManagedSurfaces(t *testing.T) {
	root := newSpeechCmd()
	engine, _, err := root.Find([]string{"engine"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"setup", "upgrade", "repair", "rollback", "remove", "status"} {
		if command, _, findErr := engine.Find([]string{name}); findErr != nil || command.Name() != name {
			t.Fatalf("engine subcommand %q missing: command=%v err=%v", name, command, findErr)
		}
	}
	voices, _, _ := root.Find([]string{"voices"})
	for _, name := range []string{"engine", "language"} {
		if voices.Flags().Lookup(name) == nil {
			t.Fatalf("voices --%s missing", name)
		}
	}
	testCmd, _, _ := root.Find([]string{"test"})
	for _, name := range []string{"engine", "voice"} {
		if testCmd.Flags().Lookup(name) == nil {
			t.Fatalf("test --%s missing", name)
		}
	}
}
