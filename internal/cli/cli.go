package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"moonglass.dev/moonglass/internal/registry"
)

const (
	requestTimeout = 30 * time.Second
	maxProblemBody = 1 << 20 // the longest error body read
)

// flagKind is the kind of a generated flag: one of the four input kinds the CLI
// can send.
type flagKind uint8

const (
	kindString flagKind = iota + 1
	kindBool
	kindInt
	kindList
)

// kindText is the flag kind as the usage text names it.
var kindText = [...]string{kindString: "string", kindBool: "bool", kindInt: "integer", kindList: "list"}

// String is the kind as the usage text names it.
func (k flagKind) String() string { return kindText[k] }

// input is one flag of a generated subcommand: an exported field of the
// operation's In.
type input struct {
	Flag     string
	JSON     string
	Doc      string
	Kind     flagKind
	Required bool
}

// command is a generated subcommand: one registry operation.
type command struct {
	Name    string
	Method  string
	Path    string
	Summary string
	Inputs  []input
}

// Subcommands lists every subcommand: generated and local.
func Subcommands() []string {
	var names []string
	for _, c := range generated {
		names = append(names, c.Name)
	}
	for _, c := range registry.LocalCommands {
		names = append(names, c.Name)
	}
	return names
}

// Usage is the help text listing every subcommand.
func Usage() string {
	var b strings.Builder
	b.WriteString("Usage: moonglass <command>\n\nCommands:\n")
	for _, c := range registry.LocalCommands {
		fmt.Fprintf(&b, "  %s\n      %s\n", c.Synopsis, c.Summary)
	}
	for _, c := range generated {
		fmt.Fprintf(&b, "  %s\n      %s (needs MOONGLASS_URL and MOONGLASS_TOKEN)\n", c.Name, c.Summary)
	}
	return b.String()
}

// Run dispatches args. local maps each registry.LocalCommands name to its
// handler, which receives the arguments after the command name; its keys must
// equal that list. It returns the process exit code.
func Run(args []string, env func(string) string, stdout, stderr io.Writer, local map[string]func(args []string) int) int {
	for _, c := range registry.LocalCommands {
		if local[c.Name] == nil {
			_, _ = fmt.Fprintf(stderr, "moonglass: no handler for local command %q: pass one for every registry.LocalCommands entry\n", c.Name)
			return 2
		}
	}
	if len(local) != len(registry.LocalCommands) {
		_, _ = fmt.Fprintf(stderr, "moonglass: %d local handlers for %d registry.LocalCommands: remove the handlers that are not on the list\n", len(local), len(registry.LocalCommands))
		return 2
	}
	best, words := "", 0
	for _, name := range Subcommands() {
		n := len(strings.Fields(name))
		if n > words && len(args) >= n && slices.Equal(args[:n], strings.Fields(name)) {
			best, words = name, n
		}
	}
	if h, ok := local[best]; ok {
		return h(args[words:])
	}
	for _, c := range generated {
		if c.Name == best {
			return runGenerated(c, args[words:], env, stdout, stderr)
		}
	}
	_, _ = io.WriteString(stderr, Usage())
	return 2
}

func runGenerated(c command, args []string, env func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(c.Name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	values := make(map[string]any, len(c.Inputs))
	badInt := ""
	for _, in := range c.Inputs {
		switch in.Kind {
		case kindString:
			fs.Func(in.Flag, in.Doc, func(s string) error {
				values[in.JSON] = s
				return nil
			})
		case kindList:
			fs.Func(in.Flag, in.Doc, func(s string) error {
				list, _ := values[in.JSON].([]string)
				values[in.JSON] = append(list, s)
				return nil
			})
		case kindInt:
			fs.Func(in.Flag, in.Doc, func(s string) error {
				v, err := strconv.ParseInt(s, 10, 64)
				if err != nil {
					badInt = in.Flag
					return err
				}
				values[in.JSON] = v
				return nil
			})
		case kindBool:
			fs.BoolFunc(in.Flag, in.Doc, func(s string) error {
				v, err := strconv.ParseBool(s)
				if err != nil {
					return err
				}
				values[in.JSON] = v
				return nil
			})
		}
	}
	if err := fs.Parse(args); err != nil {
		if badInt != "" {
			_, _ = fmt.Fprintf(stderr, "moonglass: %s: --%s must be an integer\n", c.Name, badInt)
		} else {
			_, _ = io.WriteString(stderr, commandUsage(c))
		}
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = io.WriteString(stderr, commandUsage(c))
		return 2
	}
	for _, in := range c.Inputs {
		if _, ok := values[in.JSON]; in.Required && !ok {
			_, _ = fmt.Fprintf(stderr, "moonglass: %s: --%s is required\n", c.Name, in.Flag)
			return 2
		}
	}
	base, token := strings.TrimRight(env("MOONGLASS_URL"), "/"), env("MOONGLASS_TOKEN")
	if base == "" || token == "" {
		_, _ = fmt.Fprintf(stderr, "moonglass: %s needs MOONGLASS_URL (the control-plane URL) and MOONGLASS_TOKEN (an owner token)\n", c.Name)
		return 2
	}
	query, body, err := encode(c, values)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "moonglass: %s: %v\n", c.Name, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, c.Method, base+c.Path+query, body)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "moonglass: %s: build request: %v\n", c.Name, err)
		return 1
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if c.Method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "moonglass: %s: call %s: %v\n", c.Name, base, err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		if _, err := io.Copy(stdout, resp.Body); err != nil {
			_, _ = fmt.Fprintf(stderr, "moonglass: %s: read response: %v\n", c.Name, err)
			return 1
		}
		return 0
	}
	printProblem(stderr, c.Name, resp)
	return 1
}

// commandUsage is the usage of one generated subcommand: its flags, the required
// ones first, each with its doc.
func commandUsage(c command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: moonglass %s [flags]\n\n%s\n", c.Name, c.Summary)
	if len(c.Inputs) == 0 {
		return b.String()
	}
	b.WriteString("\nFlags:\n")
	write := func(in input) {
		fmt.Fprintf(&b, "  --%s %s", in.Flag, in.Kind)
		if in.Kind == kindList {
			b.WriteString(", repeatable")
		}
		if in.Required {
			b.WriteString(" (required)")
		}
		b.WriteString("\n")
		if in.Doc != "" {
			fmt.Fprintf(&b, "      %s\n", in.Doc)
		}
	}
	for _, in := range c.Inputs {
		if in.Required {
			write(in)
		}
	}
	for _, in := range c.Inputs {
		if !in.Required {
			write(in)
		}
	}
	return b.String()
}

// encode turns the flags given into the request's query string and body: query
// parameters named by the json tags for GET, one JSON object otherwise.
func encode(c command, values map[string]any) (string, io.Reader, error) {
	if c.Method == http.MethodGet {
		q := url.Values{}
		for _, in := range c.Inputs {
			v, ok := values[in.JSON]
			if !ok {
				continue
			}
			switch v := v.(type) {
			case string:
				q.Set(in.JSON, v)
			case bool:
				q.Set(in.JSON, strconv.FormatBool(v))
			case int64:
				q.Set(in.JSON, strconv.FormatInt(v, 10))
			case []string:
				for _, s := range v {
					q.Add(in.JSON, s)
				}
			}
		}
		if len(q) == 0 {
			return "", http.NoBody, nil
		}
		return "?" + q.Encode(), http.NoBody, nil
	}
	body, err := json.Marshal(values)
	if err != nil {
		return "", nil, fmt.Errorf("encode the inputs of %s: %w", c.Name, err)
	}
	return "", bytes.NewReader(body), nil
}

// problem is the application/problem+json body of a refused call.
type problem struct {
	Detail string `json:"detail"`
	Errors []struct {
		Location string `json:"location"`
		Message  string `json:"message"`
	} `json:"errors"`
}

// printProblem prints a refused call's detail and errors entries to w, never a
// flag value or the token.
func printProblem(w io.Writer, name string, resp *http.Response) {
	var p problem
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProblemBody))
	if err == nil {
		_ = json.Unmarshal(body, &p)
	}
	detail := p.Detail
	if detail == "" {
		detail = fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	_, _ = fmt.Fprintf(w, "moonglass: %s: %s\n", name, detail)
	for _, e := range p.Errors {
		_, _ = fmt.Fprintf(w, "  %s: %s\n", e.Location, e.Message)
	}
}
