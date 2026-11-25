package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/muratcanozdemir/anypoint-go-client/anypoint"
)

// anypoint-cli-account-plugin: account:business-group:*, account:environment:*, account:user:*.

func accountCmd() *Cmd {
	return group("account", "Business groups, environments and the current user",
		group("business-group", "Business groups",
			leaf("describe", "[IDENTIFIER]", "Show a business group (default: the selected one); identifier is an ID or a name", cmdBGDescribe),
			leaf("list", "", "List business groups", cmdBGList).alias("ls"),
		).alias("bg"),
		group("environment", "Environments",
			leaf("create", "NAME", "Create an environment", cmdEnvCreate),
			leaf("delete", "NAME", "Delete an environment", cmdEnvDelete).alias("rm"),
			leaf("describe", "[NAME]", "Show an environment (default: the selected one)", cmdEnvDescribe),
			leaf("list", "", "List environments", cmdEnvList).alias("ls"),
		).alias("env"),
		group("user", "The authenticated user",
			leaf("describe", "", "Show the authenticated user", cmdUserDescribe),
		),
	)
}

func yn(b bool) string {
	if b {
		return "Y"
	}
	return "N"
}

func orgType(o anypoint.Organization) string {
	if o.IsRoot {
		return "Root"
	}
	return "Business unit"
}

func cmdBGList(x *Ctx, args []string) error {
	_, c, err := x.Setup(x.Flags(), args)
	if err != nil {
		return err
	}
	orgs, err := c.BusinessGroups(x.ctx)
	if err != nil {
		return err
	}
	return x.Print(orgs, func() {
		tw := newTable(x.Out, "NAME", "TYPE", "ID")
		for _, o := range orgs {
			tw.row(o.Name, orgType(o), o.ID)
		}
		tw.flush()
	})
}

func cmdBGDescribe(x *Ctx, args []string) error {
	pos, c, err := x.Setup(x.Flags(), args)
	if err != nil {
		return err
	}
	id := arg(pos, 0)
	if id == "" {
		if id, err = c.OrgID(x.ctx); err != nil {
			return err
		}
	} else if id, err = findBusinessGroup(x, c, id); err != nil {
		return err
	}
	org, err := c.GetOrganization(x.ctx, id)
	if err != nil {
		return err
	}
	return x.Print(org, func() {
		owner := "(not visible)"
		if org.Owner != nil && org.Owner.Username != "" {
			owner = org.Owner.Username
		}
		var envs []string
		for _, e := range org.Environments {
			envs = append(envs, e.Name)
		}
		sub := ""
		if s := org.Subscription; s != nil {
			exp := s.Expiration
			if exp == "" {
				exp = "never"
			}
			sub = fmt.Sprintf("%s / %s, expires %s", s.Category, s.Type, exp)
		}
		var ents []string
		for k := range org.Entitlements {
			ents = append(ents, k)
		}
		sort.Strings(ents)
		printKV(x.Out,
			"Id", org.ID,
			"Name", org.Name,
			"Owner", owner,
			"Type", orgType(*org),
			"Subscription", sub,
			"Environments", strings.Join(envs, ", "),
			"Entitlements", strings.Join(ents, ", ")+" (-o json for values)")
	})
}

// findBusinessGroup matches an ID first, then a name, like anypoint-cli-v4.
func findBusinessGroup(x *Ctx, c *anypoint.Client, ident string) (string, error) {
	orgs, err := c.BusinessGroups(x.ctx)
	if err != nil {
		return "", err
	}
	for _, o := range orgs {
		if o.ID == ident {
			return o.ID, nil
		}
	}
	for _, o := range orgs {
		if o.Name == ident {
			return o.ID, nil
		}
	}
	return "", fmt.Errorf("organization %s not found", ident)
}

func cmdEnvList(x *Ctx, args []string) error {
	_, c, err := x.Setup(x.Flags(), args)
	if err != nil {
		return err
	}
	envs, err := c.Envs(x.ctx)
	if err != nil {
		return err
	}
	return x.Print(envs, func() {
		tw := newTable(x.Out, "NAME", "ID", "SANDBOX")
		for _, e := range envs {
			tw.row(e.Name, e.ID, yn(!e.IsProduction))
		}
		tw.flush()
	})
}

func printEnv(x *Ctx, e *anypoint.Environment) error {
	return x.Print(e, func() {
		printKV(x.Out, "Id", e.ID, "Name", e.Name, "Organization Id", e.OrganizationID,
			"Production", yn(e.IsProduction), "Type", e.Type, "Client Id", e.ClientID)
	})
}

// findEnv returns the environment with the given name (case-insensitive) or ID.
func findEnv(x *Ctx, c *anypoint.Client, name string) (*anypoint.Environment, error) {
	envs, err := c.Envs(x.ctx)
	if err != nil {
		return nil, err
	}
	for i, e := range envs {
		if e.ID == name || strings.EqualFold(e.Name, name) {
			return &envs[i], nil
		}
	}
	return nil, fmt.Errorf("environment %s not found", name)
}

func cmdEnvCreate(x *Ctx, args []string) error {
	fs := x.Flags()
	typ := fs.String("type", "sandbox", "environment `type`: design, production or sandbox")
	aliasFlag(fs, "t", "type")
	pos, c, err := x.Setup(fs, args)
	if err != nil {
		return err
	}
	switch *typ {
	case "design", "production", "sandbox":
	default:
		return fmt.Errorf("--type flag value %s unsupported", *typ)
	}
	org, err := c.OrgID(x.ctx)
	if err != nil {
		return err
	}
	e, err := c.CreateEnvironment(x.ctx, org, &anypoint.CreateEnvironmentRequest{Name: pos[0], Type: *typ, OrganizationID: org})
	if err != nil {
		return err
	}
	if x.Output != "json" {
		fmt.Fprintf(x.Out, "Environment %s created\n", e.Name)
	}
	return printEnv(x, e)
}

func cmdEnvDelete(x *Ctx, args []string) error {
	pos, c, err := x.Setup(x.Flags(), args)
	if err != nil {
		return err
	}
	e, err := findEnv(x, c, pos[0])
	if err != nil {
		return err
	}
	org, err := c.OrgID(x.ctx)
	if err != nil {
		return err
	}
	if err := c.DeleteEnvironment(x.ctx, org, e.ID); err != nil {
		return err
	}
	return x.Done("Environment %s deleted successfully", pos[0])
}

func cmdEnvDescribe(x *Ctx, args []string) error {
	pos, c, err := x.Setup(x.Flags(), args)
	if err != nil {
		return err
	}
	name := arg(pos, 0)
	if name == "" {
		if name, err = c.EnvID(x.ctx); err != nil {
			return err
		}
	}
	e, err := findEnv(x, c, name)
	if err != nil {
		return err
	}
	return printEnv(x, e)
}

func cmdUserDescribe(x *Ctx, args []string) error {
	_, c, err := x.Setup(x.Flags(), args)
	if err != nil {
		return err
	}
	u, err := c.CurrentUser(x.ctx)
	if err != nil {
		return err
	}
	return x.Print(u, func() {
		created, _, _ := strings.Cut(u.CreatedAt, "T")
		printKV(x.Out, "Id", u.ID, "User name", u.Username,
			"Full name", strings.TrimSpace(u.FirstName+" "+u.LastName), "Email", u.Email, "Created", created)
	})
}
