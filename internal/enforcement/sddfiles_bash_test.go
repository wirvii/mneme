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

func TestEvaluateSDDBash_Ancestor(t *testing.T) {
	runSDDBlocks(t, []sddBashCase{
		{"rm -rf .mneme", "rm"},
		{"rm -r ./.mneme/", "rm"},
		{"rm -R .mneme", "rm"},
		{"rm --recursive .mneme", "rm"},
		{"mv .mneme /tmp/m", "mv"},
		{"rm -rf /w/repo", "rm"},
		{"rm -rf .", "rm"},
		{"mv /w/repo /tmp/r", "mv"},
	})
	runSDDAllows(t, []string{
		"rm -rf /w/other",
		"rm .mneme/shared/a",
		"rm -rf *",
		"rm -f /w/repo",
		"cp -r /w/repo /tmp/r",
		"rm -rf docs",
	})
}

func TestEvaluateSDDBash_Globs(t *testing.T) {
	runSDDBlocks(t, []sddBashCase{
		{"rm .mneme/*/a", "rm"},
		{"rm .m*/sdd/a", "rm"},
		{"rm .mneme/s?d/a", "rm"},
		{"rm .mneme/[s]dd/a", "rm"},
		{"echo x > .mneme/{sdd,tmp}/a", ">"},
		{"rm .mneme/{tmp,sdd}/a", "rm"},
		{"rm .mneme/s{d,x}d/a", "rm"},
		{"tee .mneme/*", "tee"},
	})
	runSDDAllows(t, []string{
		"rm *",
		"rm */sdd/a",
		"rm [.]mneme/sdd/a",
		"rm .mneme/{tmp,shared}/a",
		"rm .mneme/{sdd/a",
		"rm .mneme/{{sdd,x},y}/a",
		"rm .mneme/[/a",
	})
}

func TestEvaluateSDDBash_Cd(t *testing.T) {
	runSDDBlocks(t, []sddBashCase{
		{"cd .mneme/sdd && echo x > a", "cd"},
		{"pushd .mneme && rm -r sdd", "pushd"},
		{"cd .mneme; sed -i s/a/b/ sdd/a", "cd"},
		{"cd .mneme/sdd && bash -c 'echo x > a'", "cd"},
		{"cd /w/repo/.mneme/sdd; tee a", "cd"},
	})
	runSDDAllows(t, []string{
		"cd .mneme/sdd && cat a",
		"cd .mneme/sdd && ls",
		"cd internal && echo x > a",
		"cd .mneme/shared && echo x > a",
	})
}

func TestGlobSegment(t *testing.T) {
	tests := []struct {
		seg, lit string
		want     bool
	}{
		{"*", ".mneme", false},
		{"*", "sdd", true},
		{".m*", ".mneme", true},
		{"[.]mneme", ".mneme", false},
		{"?mneme", ".mneme", false},
		{".MNEME", ".mneme", true},
		{"S*", "sdd", true},
		{"[", "sdd", false},
		{"{sdd,tmp}", "sdd", true},
		{"{a,b}", "sdd", false},
		{"{", "sdd", false},
		{"{{sdd,x},y}", "sdd", false},
	}
	for _, tt := range tests {
		if got := globSegment(tt.seg, tt.lit); got != tt.want {
			t.Errorf("globSegment(%q, %q) = %v, want %v", tt.seg, tt.lit, got, tt.want)
		}
	}
}

func TestEvaluateSDDBash_ExtraForms(t *testing.T) {
	runSDDBlocks(t, []sddBashCase{
		{"touch .mneme/sdd/a", "touch"},
		{"touch -d yesterday .mneme/sdd/a", "touch"},
		{"rmdir .mneme/sdd/specs/SPEC-1", "rmdir"},
		{"rmdir .mneme", "rmdir"},
		{"ln -s /tmp/x .mneme/sdd/a", "ln"},
		{"install /tmp/x .mneme/sdd/a", "install"},
		{"install -m 644 /tmp/x .mneme/sdd/a", "install"},
		{"install -d .mneme/sdd/n", "install"},
		{"dd if=/tmp/x of=.mneme/sdd/a", "dd"},
		{"perl -pi -e 's/a/b/' .mneme/sdd/a", "perl -i"},
		{"perl -i.bak -pe 's/a/b/' .mneme/sdd/a", "perl -i"},
		{`python3 -c "open('.mneme/sdd/a','w').write('x')"`, "python -c"},
		{`python -c "import shutil; shutil.copy('/tmp/x', '.mneme/sdd/a')"`, "python -c"},
		{`node -e "require('fs').writeFileSync('.mneme/sdd/a','x')"`, "node -e"},
	})
	runSDDAllows(t, []string{
		`python3 -c "print(open('.mneme/sdd/a').read())"`,
		`python3 -c "open('/tmp/a','w').write('x')"`,
		`node -e "console.log(1)"`,
		"python3 script.py .mneme/sdd/a",
		"python3 -c",
		"chmod 644 .mneme/sdd/a",
		"chown me .mneme/sdd/a",
		"patch -p1 < .mneme/sdd/a.patch",
		"ln -s .mneme/sdd/x /tmp/l",
		"perl -e 'print 1' .mneme/sdd/a",
		"touch /tmp/a",
		"install -d /tmp/n",
		"dd if=.mneme/sdd/a of=/tmp/x",
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
