package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/muratcanozdemir/anypoint-go-client/anypoint"
)

// Cmd is a node in the command tree. Leaf nodes have Run; groups have Sub.
//
// Args is the positional usage in anypoint-cli-v4 notation: NAME is
// required, [NAME] optional, and a trailing "..." repeats; Parse derives the
// accepted argument count from it.
type Cmd struct {
	Name    string
	Aliases []string
	Args    string
	Short   string
	Sub     []*Cmd
	Run     func(x *Ctx, args []string) error

	unsupported string // reason, for commands registered with unsupported()
}

// group and leaf keep the command tables compact.
func group(name, short string, sub ...*Cmd) *Cmd { return &Cmd{Name: name, Short: short, Sub: sub} }

func leaf(name, args, short string, run func(*Ctx, []string) error) *Cmd {
	return &Cmd{Name: name, Args: args, Short: short, Run: run}
}

// alias adds alternative names to a command and returns it.
func (c *Cmd) alias(names ...string) *Cmd { c.Aliases = append(c.Aliases, names...); return c }

// argCount derives the accepted positional count from a v4 usage string.
func argCount(spec string) (minArgs, maxArgs int) {
	for tok := range strings.FieldsSeq(spec) {
		if strings.HasSuffix(tok, "...") || strings.HasSuffix(tok, "...]") {
			if !strings.HasPrefix(tok, "[") {
				minArgs++
			}
			return minArgs, -1
		}
		if !strings.HasPrefix(tok, "[") {
			minArgs++
		}
		maxArgs++
	}
	return minArgs, maxArgs
}

// Ctx carries the global flags and lazily-built client for one invocation.
type Ctx struct {
	Path    string // e.g. "account environment create"
	Profile string
	Org     string
	Env     string
	Output  string
	Verbose bool
	Out     io.Writer
	Err     io.Writer // usage and progress messages

	cmd    *Cmd
	ctx    context.Context
	flags  Profile // credentials and host given as flags (anypoint-cli-v4 names)
	bearer string
	cfg    *Config
	prof   Profile // effective settings: flags > environment > profile
	client *anypoint.Client
}

var errHelp = errors.New("help")

// globalFlags are accepted by every command and listed once, in the root help.
var globalFlags = map[string]bool{
	"profile": true, "org": true, "organization": true, "env": true, "environment": true,
	"o": true, "output": true, "verbose": true, "host": true, "client_id": true, "client_secret": true,
	"username": true, "password": true, "bearer": true, "collectMetrics": true,
}

// Flags creates a flag set that already contains the global flags, including
// the anypoint-cli-v4 spellings.
func (x *Ctx) Flags() *flag.FlagSet {
	fs := flag.NewFlagSet(x.Path, flag.ContinueOnError)
	fs.StringVar(&x.Profile, "profile", x.Profile, "config profile `name` (env ANYPOINT_PROFILE)")
	fs.StringVar(&x.Org, "org", x.Org, "organization / business group `name or ID`")
	fs.StringVar(&x.Env, "env", x.Env, "environment `name or ID`")
	fs.StringVar(&x.Output, "o", x.Output, "output `format`: table|json")
	fs.BoolVar(&x.Verbose, "verbose", x.Verbose, "log HTTP requests to stderr (-v unless the command uses it)")
	aliasFlag(fs, "organization", "org")
	aliasFlag(fs, "environment", "env")
	aliasFlag(fs, "output", "o")
	fs.StringVar(&x.flags.Host, "host", x.flags.Host, "control plane")
	fs.StringVar(&x.flags.ClientID, "client_id", x.flags.ClientID, "connected app client ID")
	fs.StringVar(&x.flags.ClientSecret, "client_secret", x.flags.ClientSecret, "connected app client secret")
	fs.StringVar(&x.flags.Username, "username", x.flags.Username, "username")
	fs.StringVar(&x.flags.Password, "password", x.flags.Password, "password")
	fs.StringVar(&x.bearer, "bearer", x.bearer, "bearer token")
	fs.Bool("collectMetrics", false, "ignored (anypoint-cli-v4 compatibility)")
	fs.SetOutput(x.Err)
	fs.Usage = func() { x.usage(fs) }
	return fs
}

// aliasFlag registers alias as another name for the existing flag name.
func aliasFlag(fs *flag.FlagSet, alias, name string) {
	fs.Var(fs.Lookup(name).Value, alias, "same as -"+name)
}

func (x *Ctx) usage(fs *flag.FlagSet) {
	w := fs.Output()
	args := ""
	if x.cmd != nil {
		args = x.cmd.Args
		if x.cmd.Short != "" {
			fmt.Fprintf(w, "%s\n\n", x.cmd.Short)
		}
	}
	fmt.Fprintf(w, "Usage: anypoint %s [flags] %s\n", x.Path, args)
	var own []*flag.Flag
	fs.VisitAll(func(f *flag.Flag) {
		if !globalFlags[f.Name] && f.Usage != "same as -verbose" {
			own = append(own, f)
		}
	})
	if len(own) > 0 {
		fmt.Fprintln(w, "\nFlags:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, f := range own {
			name, usage := flag.UnquoteUsage(f)
			dash := "--"
			if len(f.Name) == 1 {
				dash = "-"
			}
			line := "  " + dash + f.Name
			if name != "" {
				line += " " + name
			}
			if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" && f.DefValue != "[]" {
				usage += " (default " + f.DefValue + ")"
			}
			fmt.Fprintf(tw, "%s\t%s\n", line, usage)
		}
		tw.Flush()
	}
	fmt.Fprintln(w, "\nGlobal flags: run `anypoint -h`.")
}

// Parse parses flags anywhere among positionals and checks the positional
// count against the command's Args.
func (x *Ctx) Parse(fs *flag.FlagSet, args []string) ([]string, error) {
	if fs.Lookup("v") == nil {
		aliasFlag(fs, "v", "verbose")
	}
	minArgs, maxArgs := 0, 0
	if x.cmd != nil {
		minArgs, maxArgs = argCount(x.cmd.Args)
	}
	var tail []string
	for i, a := range args {
		if a == "--" {
			args, tail = args[:i], args[i+1:]
			break
		}
	}
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, errHelp
			}
			return nil, err
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	pos = append(pos, tail...)
	if len(pos) < minArgs || (maxArgs >= 0 && len(pos) > maxArgs) {
		fs.Usage()
		return nil, fmt.Errorf("%s: wrong number of arguments", x.Path)
	}
	if x.Output != "table" && x.Output != "json" {
		return nil, fmt.Errorf("invalid -o %q: want table or json", x.Output)
	}
	return pos, nil
}

// Setup parses the flags and builds the client: the common start of a command.
func (x *Ctx) Setup(fs *flag.FlagSet, args []string) ([]string, *anypoint.Client, error) {
	pos, err := x.Parse(fs, args)
	if err != nil {
		return nil, nil, err
	}
	c, err := x.Client()
	return pos, c, err
}

// OrgEnv resolves the selected organization and environment IDs.
func (x *Ctx) OrgEnv(c *anypoint.Client) (orgID, envID string, err error) {
	if orgID, err = c.OrgID(x.ctx); err != nil {
		return "", "", err
	}
	envID, err = c.EnvID(x.ctx)
	return orgID, envID, err
}

// arg returns the i-th positional, or "" when it was not given.
func arg(pos []string, i int) string {
	if i < len(pos) {
		return pos[i]
	}
	return ""
}

func (x *Ctx) Config() (*Config, error) {
	if x.cfg == nil {
		c, err := loadConfig()
		if err != nil {
			return nil, err
		}
		x.cfg = c
	}
	return x.cfg, nil
}

// ProfileName is the profile selected by --profile, $ANYPOINT_PROFILE or the config's current.
func (x *Ctx) ProfileName() string {
	if x.Profile != "" {
		return x.Profile
	}
	if p := os.Getenv("ANYPOINT_PROFILE"); p != "" {
		return p
	}
	if c, err := x.Config(); err == nil {
		return c.Current
	}
	return "default"
}

// Client builds the API client: flags > environment > profile.
func (x *Ctx) Client() (*anypoint.Client, error) {
	if x.client != nil {
		return x.client, nil
	}
	cfg, err := x.Config()
	if err != nil {
		return nil, err
	}
	p := cfg.profile(x.ProfileName()).overlayEnv()
	for _, k := range profileKeys {
		if src, _ := x.flags.field(k); *src != "" {
			dst, _ := p.field(k)
			*dst = *src
		}
	}
	if x.Org != "" {
		p.Org = x.Org
	}
	if x.Env != "" {
		p.Env = x.Env
	}
	x.prof = p
	opts := []anypoint.Option{
		anypoint.WithClientCredentials(p.ClientID, p.ClientSecret),
		anypoint.WithUserCredentials(p.Username, p.Password),
		anypoint.WithOrg(p.Org),
		anypoint.WithEnv(p.Env),
		anypoint.WithUserAgent("anypoint-cli/" + version),
	}
	token := x.bearer
	for _, n := range []string{"ANYPOINT_BEARER", "ANYPOINT_TOKEN"} {
		if token == "" {
			token = os.Getenv(n)
		}
	}
	if token != "" {
		opts = append(opts, anypoint.WithToken(token))
	}
	if d, err := os.UserCacheDir(); err == nil {
		opts = append(opts, anypoint.WithTokenCache(filepath.Join(d, "anypoint")))
	}
	if x.Verbose {
		opts = append(opts, anypoint.WithDebug(os.Stderr))
	}
	x.client = anypoint.NewClient(p.Host, opts...)
	return x.client, nil
}

// Print renders v as JSON with -o json (the API's own JSON for models),
// otherwise calls table.
func (x *Ctx) Print(v any, table func()) error {
	if x.Output == "json" || table == nil {
		return printJSON(x.Out, v)
	}
	table()
	return nil
}

// Done prints a confirmation, or {"message": ...} with -o json.
func (x *Ctx) Done(format string, a ...any) error {
	msg := fmt.Sprintf(format, a...)
	return x.Print(map[string]string{"message": msg}, func() { fmt.Fprintln(x.Out, msg) })
}

func (c *Cmd) find(name string) *Cmd {
	for _, s := range c.Sub {
		if s.Name == name || slices.Contains(s.Aliases, name) {
			return s
		}
	}
	return nil
}

func (c *Cmd) help(w io.Writer, path string) {
	if c.Short != "" {
		fmt.Fprintf(w, "%s\n\n", c.Short)
	}
	fmt.Fprintf(w, "Usage: %s <command> [flags]\n\nCommands:\n", strings.TrimSpace("anypoint "+path))
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	for _, s := range c.Sub {
		usage := s.Args
		if s.Sub != nil {
			usage = "<command>"
		}
		short := s.Short
		if len(s.Aliases) > 0 {
			short += " [" + strings.Join(s.Aliases, ", ") + "]"
		}
		fmt.Fprintf(tw, "  %s\t%s\n", strings.TrimSpace(s.Name+" "+usage), short)
	}
	tw.Flush()
	if path == "" {
		fmt.Fprint(w, rootHelpFooter)
	} else {
		fmt.Fprintf(w, "\nRun `anypoint %s <command> -h` for flags.\n", path)
	}
}

const rootHelpFooter = `
Commands can also be written as in anypoint-cli-v4: anypoint account:environment:list

Global flags (accepted by every command):
  --profile NAME              config profile
  --org, --organization NAME  business group (name or ID)
  --env, --environment NAME   environment (name or ID)
  -o, --output table|json     output format
  -v, --verbose               log HTTP requests
  --host, --client_id, --client_secret, --username, --password, --bearer
                              credentials, overriding the profile and environment

Credentials come from flags, then ANYPOINT_* environment variables
(ANYPOINT_CLIENT_ID, ANYPOINT_CLIENT_SECRET, ANYPOINT_USERNAME, ANYPOINT_PASSWORD,
ANYPOINT_HOST, ANYPOINT_ORG, ANYPOINT_ENV, ANYPOINT_BEARER), then the config profile.
`

// resolve walks the command tree. A first argument written as in
// anypoint-cli-v4 ("account:environment:list") is split into words.
func resolve(root *Cmd, args []string) (node *Cmd, path string, rest []string, err error) {
	if len(args) > 0 && strings.Contains(args[0], ":") && !strings.HasPrefix(args[0], "-") && !strings.Contains(args[0], "/") {
		args = append(strings.Split(args[0], ":"), args[1:]...)
	}
	node = root
	for len(args) > 0 && node.Sub != nil {
		if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
			return node, path, args, nil
		}
		next := node.find(args[0])
		if next == nil {
			return node, path, args, fmt.Errorf("unknown command %q", strings.TrimSpace(path+" "+args[0]))
		}
		node, path, args = next, strings.TrimSpace(path+" "+next.Name), args[1:]
	}
	return node, path, args, nil
}

func runContext(ctx context.Context, args []string, out, errOut io.Writer) error {
	node, path, args, err := resolve(commands(), args)
	if err != nil {
		node.help(errOut, path)
		return err
	}
	if node.Run == nil {
		node.help(out, path)
		return nil
	}
	x := &Ctx{Path: path, Output: "table", Out: out, Err: errOut, ctx: ctx, cmd: node}
	return hint(node.Run(x, args))
}

func run(args []string, out io.Writer) error {
	return runContext(context.Background(), args, out, os.Stderr)
}

// unsupported registers an anypoint-cli-v4 command that this CLI does not
// implement; running it explains why.
func unsupported(name, args, reason string) *Cmd {
	return &Cmd{Name: name, Args: args, Short: "not supported: " + reason, Run: func(x *Ctx, _ []string) error {
		return fmt.Errorf("anypoint %s is not supported: %s", x.Path, reason)
	}, unsupported: reason}
}

// hint adds CLI advice to the library's sentinel errors.
func hint(err error) error {
	switch {
	case errors.Is(err, anypoint.ErrNoCredentials):
		return fmt.Errorf("%w: run `anypoint login --client_id ID --client_secret SECRET` "+
			"or set ANYPOINT_CLIENT_ID/ANYPOINT_CLIENT_SECRET (or ANYPOINT_USERNAME/ANYPOINT_PASSWORD)", err)
	case errors.Is(err, anypoint.ErrNoOrganization):
		return fmt.Errorf("%w; pass --organization", err)
	case errors.Is(err, anypoint.ErrNoEnvironment):
		return fmt.Errorf("%w: pass --environment NAME or run `anypoint config set env NAME`", err)
	}
	return err
}

// multiFlag is a repeatable string flag (oclif's multiple: true).
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }

func (m *multiFlag) Set(s string) error { *m = append(*m, s); return nil }

// multi registers a repeatable string flag.
func multi(fs *flag.FlagSet, name, usage string) *multiFlag {
	m := &multiFlag{}
	fs.Var(m, name, usage)
	return m
}

// need fails with the first missing required flag of name/value pairs.
func need(pairs ...string) error {
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] == "" {
			return fmt.Errorf("missing required flag --%s", pairs[i])
		}
	}
	return nil
}

// oneOf checks a flag's value against its allowed values ("" passes).
func oneOf(name, value string, allowed ...string) error {
	if value == "" || slices.Contains(allowed, value) {
		return nil
	}
	return fmt.Errorf("--%s must be one of %s, got %q", name, strings.Join(allowed, ", "), value)
}
