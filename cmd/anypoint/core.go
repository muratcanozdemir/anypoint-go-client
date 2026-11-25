package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/muratcanozdemir/anypoint-go-client/anypoint"
)

// ---------------------------------------------------------------- login / whoami

// cmdLogin checks the credentials given as flags (or in the environment) and
// saves the flags into the current profile.
func cmdLogin(x *Ctx, args []string) error {
	if _, err := x.Parse(x.Flags(), args); err != nil {
		return err
	}
	cfg, err := x.Config()
	if err != nil {
		return err
	}
	saved := cfg.profile(x.ProfileName())
	changed := false
	for _, k := range profileKeys {
		src, _ := x.flags.field(k)
		if *src == "" {
			continue
		}
		dst, _ := saved.field(k)
		*dst, changed = *src, true
	}
	if x.Org != "" {
		saved.Org, changed = x.Org, true
	}
	if x.Env != "" {
		saved.Env, changed = x.Env, true
	}
	c, err := x.Client()
	if err != nil {
		return err
	}
	if err := c.Login(x.ctx); err != nil {
		return err
	}
	if changed {
		if err := cfg.save(); err != nil {
			return err
		}
		fmt.Fprintf(x.Err, "saved profile %q to %s\n", x.ProfileName(), cfg.path)
	}
	return whoami(x)
}

func cmdLogout(x *Ctx, args []string) error {
	_, c, err := x.Setup(x.Flags(), args)
	if err != nil {
		return err
	}
	return c.Logout()
}

func cmdWhoami(x *Ctx, args []string) error {
	if _, err := x.Parse(x.Flags(), args); err != nil {
		return err
	}
	return whoami(x)
}

func whoami(x *Ctx) error {
	c, err := x.Client()
	if err != nil {
		return err
	}
	me, err := c.Me(x.ctx)
	if err != nil {
		return err
	}
	return x.Print(me, func() {
		org, _ := c.OrgID(x.ctx)
		who := str(dig(me, "user.username"))
		if who == "" {
			who = "connected app " + str(dig(me, "client.name")) + " (" + str(dig(me, "client.client_id")) + ")"
		}
		printKV(x.Out,
			"User", who,
			"Name", strings.TrimSpace(str(dig(me, "user.firstName"))+" "+str(dig(me, "user.lastName"))),
			"Email", str(dig(me, "user.email")),
			"Organization", org,
			"Environment", x.prof.Env,
			"Host", c.BaseURL(),
			"Profile", x.ProfileName())
	})
}

// ---------------------------------------------------------------- config

func cmdConfigList(x *Ctx, args []string) error {
	if _, err := x.Parse(x.Flags(), args); err != nil {
		return err
	}
	cfg, err := x.Config()
	if err != nil {
		return err
	}
	cur := x.ProfileName()
	var rows []map[string]any
	for _, n := range cfg.names() {
		p := cfg.Profiles[n]
		mark := ""
		if n == cur {
			mark = "*"
		}
		auth := p.ClientID
		if auth == "" {
			auth = p.Username
		}
		rows = append(rows, map[string]any{"cur": mark, "name": n, "host": anypoint.HostURL(p.Host), "auth": auth, "org": p.Org, "env": p.Env})
	}
	return x.Print(rows, func() {
		printTable(x.Out, rows, col("", "cur"), col("PROFILE", "name"), col("HOST", "host"), col("AUTH", "auth"), col("ORG", "org"), col("ENV", "env"))
	})
}

func cmdConfigGet(x *Ctx, args []string) error {
	pos, err := x.Parse(x.Flags(), args)
	if err != nil {
		return err
	}
	cfg, err := x.Config()
	if err != nil {
		return err
	}
	p := cfg.profile(x.ProfileName())
	if len(pos) == 1 {
		f, err := p.field(pos[0])
		if err != nil {
			return err
		}
		fmt.Fprintln(x.Out, *f)
		return nil
	}
	var kv []string
	for _, k := range profileKeys {
		v, _ := p.field(k)
		s := *v
		if isSecretKey(k) && s != "" {
			s = "********"
		}
		kv = append(kv, k, s)
	}
	fmt.Fprintf(x.Out, "# profile %q (%s)\n", x.ProfileName(), cfg.path)
	printKV(x.Out, kv...)
	return nil
}

func cmdConfigSet(x *Ctx, args []string) error {
	pos, err := x.Parse(x.Flags(), args)
	if err != nil {
		return err
	}
	cfg, err := x.Config()
	if err != nil {
		return err
	}
	f, err := cfg.profile(x.ProfileName()).field(pos[0])
	if err != nil {
		return err
	}
	val := pos[1]
	if val == "-" {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return fmt.Errorf("read value from stdin: %w", err)
		}
		val = strings.TrimRight(line, "\r\n")
	}
	*f = val
	return cfg.save()
}

func cmdConfigUnset(x *Ctx, args []string) error {
	pos, err := x.Parse(x.Flags(), args)
	if err != nil {
		return err
	}
	cfg, err := x.Config()
	if err != nil {
		return err
	}
	f, err := cfg.profile(x.ProfileName()).field(pos[0])
	if err != nil {
		return err
	}
	*f = ""
	return cfg.save()
}

func cmdConfigUse(x *Ctx, args []string) error {
	pos, err := x.Parse(x.Flags(), args)
	if err != nil {
		return err
	}
	cfg, err := x.Config()
	if err != nil {
		return err
	}
	cfg.profile(pos[0])
	cfg.Current = pos[0]
	return cfg.save()
}
