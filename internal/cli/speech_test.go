package cli

import "testing"

func TestSpeechCommandSurfaceIsNativeOnly(t *testing.T) {
	root := newSpeechCmd()

	t.Run("engine subcommand does not exist", func(t *testing.T) {
		command, _, err := root.Find([]string{"engine"})
		if err == nil && command != nil && command.Name() == "engine" {
			t.Fatal("speech engine subcommand still exists")
		}
	})

	t.Run("surviving subcommands exist", func(t *testing.T) {
		for _, name := range []string{"on", "off", "stop", "status", "mode", "voice", "test", "voices", "setup", "supervise"} {
			if command, _, err := root.Find([]string{name}); err != nil || command.Name() != name {
				t.Fatalf("speech subcommand %q missing: command=%v err=%v", name, command, err)
			}
		}
	})

	t.Run("test keeps mode and voice, drops engine", func(t *testing.T) {
		testCmd, _, err := root.Find([]string{"test"})
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"mode", "voice"} {
			if testCmd.Flags().Lookup(name) == nil {
				t.Fatalf("test --%s missing", name)
			}
		}
		if testCmd.Flags().Lookup("engine") != nil {
			t.Fatal("test --engine should have been removed")
		}
	})

	t.Run("voices keeps engine and json, drops language", func(t *testing.T) {
		voices, _, err := root.Find([]string{"voices"})
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"engine", "json"} {
			if voices.Flags().Lookup(name) == nil {
				t.Fatalf("voices --%s missing", name)
			}
		}
		if voices.Flags().Lookup("language") != nil {
			t.Fatal("voices --language should have been removed")
		}
	})

	t.Run("on drops yes and native", func(t *testing.T) {
		on, _, err := root.Find([]string{"on"})
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"yes", "native"} {
			if on.Flags().Lookup(name) != nil {
				t.Fatalf("on --%s should have been removed", name)
			}
		}
	})
}
