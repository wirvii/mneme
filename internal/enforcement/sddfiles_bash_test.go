package enforcement

import "testing"

// sddBashContext is the context the Bash tables share: a repository at
// /w/repo, and a HasSDDDir that knows only that one.
func sddBashContext() SDDContext {
	c := sddTestContext("linux", "/w/repo")
	c.HasSDDDir = func(abs string) bool { return abs == "/w/repo" }
	return c
}

type sddBashCase struct {
	cmd  string
	form string
}

func runSDDBlocks(t *testing.T, cases []sddBashCase) {
	t.Helper()
	c := sddBashContext()
	for _, tt := range cases {
		t.Run(tt.cmd, func(t *testing.T) {
			got := EvaluateSDDBash(tt.cmd, c)
			if !got.Block {
				t.Fatalf("EvaluateSDDBash(%q) did not block", tt.cmd)
			}
			if tt.form != "" && got.Form != tt.form {
				t.Errorf("EvaluateSDDBash(%q).Form = %q, want %q", tt.cmd, got.Form, tt.form)
			}
			if got.Target == "" {
				t.Errorf("EvaluateSDDBash(%q) returned no Target", tt.cmd)
			}
		})
	}
}

func runSDDAllows(t *testing.T, cmds []string) {
	t.Helper()
	c := sddBashContext()
	for _, cmd := range cmds {
		t.Run(cmd, func(t *testing.T) {
			if got := EvaluateSDDBash(cmd, c); got.Block {
				t.Errorf("EvaluateSDDBash(%q) blocked: %+v", cmd, got)
			}
		})
	}
}

func TestEvaluateSDDBash_Blocks(t *testing.T) {
	runSDDBlocks(t, []sddBashCase{
		{"echo x > .mneme/sdd/a.md", ">"},
		{"echo x >> .mneme/sdd/a.md", ">>"},
		{"echo x >| .mneme/sdd/a", ">|"},
		{"cmd &> .mneme/sdd/a", "&>"},
		{"cmd 2> .mneme/sdd/a", "2>"},
		{"sed -i 's/a/b/' .mneme/sdd/a.md", "sed -i"},
		{"sed -i.bak 's/a/b/' .mneme/sdd/a.md", "sed -i"},
		{"sed --in-place 's/a/b/' .mneme/sdd/a.md", "sed -i"},
		{"sed --in-place=.bak 's/a/b/' .mneme/sdd/a.md", "sed -i"},
		{"sed -Ei 's/a/b/' .mneme/sdd/a.md", "sed -i"},
		{"sed -i -e 's/a/b/' .mneme/sdd/a.md", "sed -i"},
		{"echo x | tee .mneme/sdd/a", "tee"},
		{"tee -a /tmp/x .mneme/sdd/a", "tee"},
		{"cp /tmp/x .mneme/sdd/a", "cp"},
		{"cp -t .mneme/sdd /tmp/x", "cp"},
		{"cp --target-directory=.mneme/sdd /tmp/x", "cp"},
		{"mv .mneme/sdd/a /tmp/a", "mv"},
		{"mv /tmp/a .mneme/sdd/a", "mv"},
		{"rm .mneme/sdd/a", "rm"},
		{"rm -- .mneme/sdd/a", "rm"},
		{"rm -f /tmp/a .mneme/sdd/a", "rm"},
		{"truncate -s 0 .mneme/sdd/a", "truncate"},
		{"truncate --size=0 .mneme/sdd/a", "truncate"},
		{"cp -r /tmp/sdd .mneme/", "cp"},
		{"mv /tmp/sdd .mneme", "mv"},
		{"echo x > /w/repo/.mneme/sdd/a", ">"},
		{"echo x > ~/repo/.mneme/sdd/a", ">"},
		{`echo x > ".mneme/sdd/a b"`, ">"},
	})
}

func TestEvaluateSDDBash_Allows(t *testing.T) {
	runSDDAllows(t, []string{
		"cat .mneme/sdd/a.md",
		"grep -r status .mneme/sdd",
		"ls -la .mneme/sdd",
		"head .mneme/sdd/a",
		"sed -n 1p .mneme/sdd/a",
		"sed 's/a/b/' .mneme/sdd/a",
		"cp .mneme/sdd/a /tmp/a",
		"wc -l < .mneme/sdd/a",
		"truncate -r .mneme/sdd/a /tmp/b",
		"git diff .mneme/sdd",
		"git log -- .mneme/sdd",
		"git pull",
		"git checkout main",
		"git checkout -- .mneme/sdd/a",
		"mneme sdd export",
		"mneme sdd import",
		"mneme spec status SPEC-1",
		"cp /tmp/x .mneme/shared/a.md",
		"rm -rf *",
		"echo x > /tmp/y 2>&1",
		"echo 'sin cerrar",
		"",
		"echo hola",
		"cp /tmp/x",
		"sed -i 's/.mneme\\/sdd/x/' internal/x.go",
	})
}

func TestEvaluateSDDBash_Compound(t *testing.T) {
	w := ".mneme/sdd/a"
	runSDDBlocks(t, []sddBashCase{
		{"echo hi; echo x > " + w, ">"},
		{"true && echo x > " + w, ">"},
		{"false || echo x > " + w, ">"},
		{"echo x | tee " + w, "tee"},
		{"( echo x > " + w + " )", ">"},
		{"{ echo x; } > " + w, ">"},
		{"{ echo a; } > .mneme/sdd/a", ">"},
		{"for i in 1; do echo $i; done >> .mneme/sdd/a", ">>"},
		{"if true; then rm " + w + "; fi", "rm"},
		{"while true; do rm " + w + "; done", "rm"},
		{"for i in 1; do cp /tmp/x " + w + "; done", "cp"},
		{"echo $(rm " + w + ")", "rm"},
		{"echo `rm " + w + "`", "rm"},
		{"echo \"$(cat x > " + w + ")\"", ">"},
		{`bash -c "echo x > ` + w + `"`, ">"},
		{`sh -c 'rm ` + w + `'`, "rm"},
		{`zsh -c "tee ` + w + `"`, "tee"},
		{"env X=1 rm " + w, "rm"},
		{"nohup rm " + w, "rm"},
		{"command rm " + w, "rm"},
		{"exec rm " + w, "rm"},
		{"timeout 5 rm " + w, "rm"},
		{"/bin/rm " + w, "rm"},
		{"f() { rm " + w + "; }; f", "rm"},
		{"case x in x) rm " + w + ";; esac", "rm"},
	})
}
