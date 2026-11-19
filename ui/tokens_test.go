package ui

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// oklch is one colour value from docs/specs/ui-tokens.css.
type oklch struct {
	L, C, H float64
}

var (
	// oklchDeclPattern matches a custom property whose value is a plain
	// oklch() triple, such as "--gray-1: oklch(0.115 0.004 260);".
	oklchDeclPattern = regexp.MustCompile(`--([a-z0-9-]+):\s*oklch\(([0-9.]+) ([0-9.]+) ([0-9.]+)\);`)
	// varDeclPattern matches a custom property aliased to another one.
	varDeclPattern = regexp.MustCompile(`--([a-z0-9-]+):\s*var\(--([a-z0-9-]+)\);`)
	// grayStepPattern matches a neutral ramp step name, "gray-1" to "gray-11".
	grayStepPattern = regexp.MustCompile(`^gray-[0-9]+$`)
)

// cssBlock returns the body of the block whose opening line equals openLine,
// up to the first closing brace at the same indentation. Indentation in
// openLine itself is ignored, so both "  :root {" and ":root {" find their
// block. It fails the test when the line or the closing brace is missing, so a
// test can never pass on an empty body.
func cssBlock(t *testing.T, css, openLine string) string {
	t.Helper()

	lines := strings.Split(css, "\n")
	want := strings.TrimSpace(openLine)
	start := -1
	indent := 0

	for i, line := range lines {
		if strings.TrimSpace(line) != want {
			continue
		}
		indent = len(line) - len(strings.TrimLeft(line, " \t"))
		start = i + 1
		break
	}
	if start < 0 {
		t.Fatalf("block %q not found in the CSS, want a line equal to it", openLine)
	}

	for i := start; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) != "}" {
			continue
		}
		if len(line)-len(strings.TrimLeft(line, " \t")) != indent {
			continue
		}
		return strings.Join(lines[start:i], "\n")
	}

	t.Fatalf("block %q has no closing brace at indentation %d, want a complete block", openLine, indent)

	return ""
}

// oklchDecls maps every custom property in block that holds an oklch() triple
// to its parsed value, keyed without the leading dashes.
func oklchDecls(t *testing.T, block string) map[string]oklch {
	t.Helper()

	decls := make(map[string]oklch)
	for _, match := range oklchDeclPattern.FindAllStringSubmatch(block, -1) {
		var values [3]float64
		for i := range values {
			value, err := strconv.ParseFloat(match[i+2], 64)
			if err != nil {
				t.Fatalf("--%s: parse %q as a number: %v", match[1], match[i+2], err)
			}
			values[i] = value
		}
		decls[match[1]] = oklch{L: values[0], C: values[1], H: values[2]}
	}

	return decls
}

// varDecls maps every custom property in block that aliases another to that
// alias, keyed without the leading dashes: --surface-frame becomes
// "surface-frame" -> "--gray-1".
func varDecls(block string) map[string]string {
	decls := make(map[string]string)
	for _, match := range varDeclPattern.FindAllStringSubmatch(block, -1) {
		decls[match[1]] = "--" + match[2]
	}

	return decls
}

// linearSRGB converts an OKLCH colour to linear sRGB with Ottosson's OKLab
// matrices, clipping every channel to [0, 1] because WCAG luminance is defined
// on displayable colours.
func linearSRGB(c oklch) [3]float64 {
	radians := c.H * math.Pi / 180
	a := c.C * math.Cos(radians)
	b := c.C * math.Sin(radians)

	l := c.L + 0.3963377774*a + 0.2158037573*b
	m := c.L - 0.1055613458*a - 0.0638541728*b
	s := c.L - 0.0894841775*a - 1.2914855480*b

	l, m, s = l*l*l, m*m*m, s*s*s

	return [3]float64{
		clip(4.0767416621*l - 3.3077115913*m + 0.2309699292*s),
		clip(-1.2684380046*l + 2.6097574011*m - 0.3413193965*s),
		clip(-0.0041960863*l - 0.7034186147*m + 1.7076147010*s),
	}
}

func clip(channel float64) float64 {
	return math.Max(0, math.Min(1, channel))
}

// luminance is the WCAG 2.2 relative luminance of a colour.
func luminance(c oklch) float64 {
	channels := linearSRGB(c)

	return 0.2126*channels[0] + 0.7152*channels[1] + 0.0722*channels[2]
}

// contrast is the WCAG 2.2 contrast ratio of two colours.
func contrast(a, b oklch) float64 {
	first, second := luminance(a), luminance(b)
	high, low := math.Max(first, second), math.Min(first, second)

	return (high + 0.05) / (low + 0.05)
}

// themes returns the resolved token values per theme, keyed "dark" and
// "light". Dark is the :root block; light is dark overlaid with the explicit
// [data-theme="light"] block, which must match the prefers-color-scheme block.
func themes(t *testing.T) map[string]map[string]oklch {
	t.Helper()

	css := readFile(t, "src/lib/tokens.css")
	dark := oklchDecls(t, cssBlock(t, css, ":root {"))

	light := make(map[string]oklch, len(dark))
	for name, value := range dark {
		light[name] = value
	}
	for name, value := range oklchDecls(t, cssBlock(t, css, `:root[data-theme="light"] {`)) {
		light[name] = value
	}

	return map[string]map[string]oklch{"dark": dark, "light": light}
}

func TestTokensByteIdenticalToSpec(t *testing.T) {
	const (
		tokensPath = "src/lib/tokens.css"
		specPath   = "../docs/specs/ui-tokens.css"
	)

	actual, err := os.ReadFile(tokensPath)
	if err != nil {
		t.Fatalf("read %s: %v, want a byte-for-byte copy of %s", tokensPath, err, specPath)
	}
	want, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v, want the token spec to compare against", specPath, err)
	}

	if !bytes.Equal(actual, want) {
		actualLines := strings.Split(string(actual), "\n")
		wantLines := strings.Split(string(want), "\n")
		for i := 0; i < len(wantLines) || i < len(actualLines); i++ {
			var actualLine, wantLine string
			if i < len(actualLines) {
				actualLine = actualLines[i]
			}
			if i < len(wantLines) {
				wantLine = wantLines[i]
			}
			if actualLine != wantLine {
				t.Fatalf("%s line %d = %q, want the spec's %q (%s line %d); the two files must be byte-identical",
					tokensPath, i+1, actualLine, wantLine, specPath, i+1)
			}
		}
		t.Fatalf("%s and %s differ in bytes but agree line by line, want byte-identical files", tokensPath, specPath)
	}

	const layoutImport = "import '$lib/tokens.css';"
	if layout := readFile(t, "src/routes/+layout.svelte"); !strings.Contains(layout, layoutImport) {
		t.Errorf("src/routes/+layout.svelte is missing %q, want the tokens reachable from the build", layoutImport)
	}
}

func TestContrastPairsMeetThresholds(t *testing.T) {
	css := readFile(t, "src/lib/tokens.css")
	resolved := themes(t)

	textPairs := []struct {
		token   string
		minimum float64
	}{
		{"gray-11", 4.5},
		{"gray-10", 4.5},
		{"gray-9", 4.5},
		{"accent", 4.5},
		{"state-success", 4.5},
		{"state-info", 4.5},
		{"state-warn", 4.5},
		{"state-danger", 4.5},
		{"state-rollback", 4.5},
		{"state-approval", 4.5},
		{"gray-8", 3},
	}

	for _, theme := range []string{"dark", "light"} {
		decls := resolved[theme]
		for _, pair := range textPairs {
			foreground, ok := decls[pair.token]
			if !ok {
				t.Fatalf("%s theme: --%s is not declared as oklch(), want the token in src/lib/tokens.css", theme, pair.token)
			}
			for step := 1; step <= 4; step++ {
				background, ok := decls[fmt.Sprintf("gray-%d", step)]
				if !ok {
					t.Fatalf("%s theme: --gray-%d is not declared as oklch(), want steps 1-4", theme, step)
				}
				if ratio := contrast(foreground, background); ratio < pair.minimum {
					t.Errorf("%s theme: --%s on --gray-%d contrast = %.2f, want >= %.2f (docs/specs/ui.md §5)",
						theme, pair.token, step, ratio, pair.minimum)
				}
			}
		}

		label, ok := decls["accent-fg"]
		if !ok {
			t.Fatalf("%s theme: --accent-fg is not declared as oklch(), want the primary button label colour", theme)
		}
		solid, ok := decls["accent-solid"]
		if !ok {
			t.Fatalf("%s theme: --accent-solid is not declared as oklch(), want the primary button fill", theme)
		}
		if ratio := contrast(label, solid); ratio < 4.5 {
			t.Errorf("%s theme: --accent-fg on --accent-solid contrast = %.2f, want >= 4.50 (docs/specs/ui.md §5)",
				theme, ratio)
		}
	}

	mediaLight := oklchDecls(t, cssBlock(t, css, `:root:not([data-theme="dark"]) {`))
	if len(mediaLight) == 0 {
		t.Fatalf("the prefers-color-scheme: light block declares no oklch() tokens, want the light theme values")
	}
	explicitLight := oklchDecls(t, cssBlock(t, css, `:root[data-theme="light"] {`))

	for name, want := range mediaLight {
		got, ok := explicitLight[name]
		if !ok {
			t.Errorf("--%s: declared in the prefers-color-scheme light block but missing from :root[data-theme=\"light\"], want both light blocks to declare the same tokens", name)
			continue
		}
		if got != want {
			t.Errorf("--%s: prefers-color-scheme light = %+v, :root[data-theme=\"light\"] = %+v, want identical oklch values", name, want, got)
		}
	}
	for name := range explicitLight {
		if _, ok := mediaLight[name]; !ok {
			t.Errorf("--%s: declared in :root[data-theme=\"light\"] but missing from the prefers-color-scheme light block, want both light blocks to declare the same tokens", name)
		}
	}

	aliases := []struct{ token, target string }{
		{"text-primary", "--gray-11"},
		{"text-secondary", "--gray-10"},
		{"text-tertiary", "--gray-9"},
		{"border-control", "--gray-8"},
		{"surface-frame", "--gray-1"},
		{"surface-canvas", "--gray-2"},
		{"surface-hover", "--gray-3"},
		{"surface-active", "--gray-4"},
	}
	semantic := varDecls(cssBlock(t, css, ":root {"))
	for _, alias := range aliases {
		if got := semantic[alias.token]; got != alias.target {
			t.Errorf("--%s = %q, want %q (docs/specs/ui.md §5)", alias.token, got, alias.target)
		}
	}
}

func TestNeutralRampChromaAndFrame(t *testing.T) {
	css := readFile(t, "src/lib/tokens.css")

	blocks := []struct{ name, openLine string }{
		{"dark root", ":root {"},
		{"prefers-color-scheme light", `:root:not([data-theme="dark"]) {`},
		{"explicit light", `:root[data-theme="light"] {`},
	}

	for _, block := range blocks {
		t.Run(block.name, func(t *testing.T) {
			decls := oklchDecls(t, cssBlock(t, css, block.openLine))

			steps := 0
			for name := range decls {
				if grayStepPattern.MatchString(name) {
					steps++
				}
			}
			if steps != 11 {
				t.Fatalf("%s declares %d gray steps, want exactly 11 (--gray-1 to --gray-11)", block.name, steps)
			}

			for step := 1; step <= 11; step++ {
				name := fmt.Sprintf("gray-%d", step)
				value, ok := decls[name]
				if !ok {
					t.Fatalf("%s is missing --%s, want a declared oklch() value", block.name, name)
				}
				if value.C > 0.008 {
					t.Errorf("%s --%s chroma = %v, want <= 0.008", block.name, name, value.C)
				}
				if value.H != 260 {
					t.Errorf("%s --%s hue = %v, want 260", block.name, name, value.H)
				}
			}

			frame, canvas := decls["gray-1"], decls["gray-2"]
			if frame.L >= canvas.L {
				t.Errorf("%s frame lightness = %v and canvas lightness = %v, want the frame deeper than the canvas (L(gray-1) < L(gray-2))",
					block.name, frame.L, canvas.L)
			}
		})
	}

	semantic := varDecls(cssBlock(t, css, ":root {"))
	for _, alias := range []struct{ token, target string }{
		{"surface-frame", "--gray-1"},
		{"surface-canvas", "--gray-2"},
	} {
		if got := semantic[alias.token]; got != alias.target {
			t.Errorf("--%s = %q, want %q in the dark :root block", alias.token, got, alias.target)
		}
	}
}

func TestOKLCHContrastConversion(t *testing.T) {
	t.Run("luminance", func(t *testing.T) {
		tests := []struct {
			name  string
			value oklch
			want  float64
		}{
			{"white", oklch{1, 0, 0}, 1},
			{"black", oklch{0, 0, 0}, 0},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				if got := luminance(test.value); math.Abs(got-test.want) > 1e-9 {
					t.Errorf("luminance(oklch(%v %v %v)) = %v, want %v", test.value.L, test.value.C, test.value.H, got, test.want)
				}
			})
		}
	})

	t.Run("contrast of white on black", func(t *testing.T) {
		white, black := oklch{1, 0, 0}, oklch{0, 0, 0}
		if got := contrast(white, black); math.Abs(got-21) > 1e-9 {
			t.Errorf("contrast(white, black) = %v, want 21", got)
		}
	})

	t.Run("oklab red", func(t *testing.T) {
		channels := linearSRGB(oklch{0.627955, 0.257683, 29.2339})
		want := [3]float64{1, 0, 0}
		for i, channel := range channels {
			if math.Abs(channel-want[i]) > 1e-3 {
				t.Errorf("linearSRGB red channel %d = %v, want %v (all three: %v, want %v)", i, channel, want[i], channels, want)
			}
		}
	})

	t.Run("out of gamut is clipped", func(t *testing.T) {
		channels := linearSRGB(oklch{0.7, 0.4, 145})
		for i, channel := range channels {
			if channel < 0 || channel > 1 {
				t.Errorf("linearSRGB(oklch(0.7 0.4 145)) channel %d = %v, want every channel clipped to [0, 1] (all three: %v)", i, channel, channels)
			}
		}
	})

	t.Run("dark text-primary on canvas matches the spec table", func(t *testing.T) {
		const want = 16.35
		ratio := contrast(oklch{0.935, 0.004, 260}, oklch{0.145, 0.005, 260})
		if math.Abs(ratio-want) > 0.05 {
			t.Errorf("dark --text-primary on --gray-2 contrast = %.4f, want %.2f ± 0.05 (docs/specs/ui.md §5)", ratio, want)
		}
	})
}
