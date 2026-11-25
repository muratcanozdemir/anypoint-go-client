package main

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/muratcanozdemir/anypoint-go-client/anypoint"
)

// mulesoft-anypoint-cli-governance-plugin. Profiles and the evaluation plan
// live in the control plane. Validating APIs and rulesets runs locally
// (governance_local.go): AMF on the JVM, and the AMF custom validator,
// which is Go, linked in. The ruleset-authoring aids (governance_authoring.go)
// read the plugin's model metadata.

func governanceCmd() *Cmd {
	return group("governance", "API Governance",
		group("api", "Governed APIs",
			leaf("evaluate", "", "List the rulesets that apply to an API (--api) or to APIs matching --criteria", cmdGovEvaluate),
			leaf("inspect", "INPUT", "List the types an API spec declares", cmdGovAPIInspect),
			leaf("validate", "SPEC", "Validate an API project (directory, zip or --remote asset) against rulesets", cmdGovAPIValidate)),
		leaf("document", "RULESET FILE", "Write a ruleset's Exchange documentation to a zip", cmdGovDocument),
		group("profile", "Governance profiles",
			leaf("create", "PROFILE-NAME RULESET-ASSET-IDENTIFIERS", "Create a profile (rulesets as group/asset/version,...)", cmdGovProfileCreate),
			leaf("delete", "PROFILE-ID", "Delete a profile", cmdGovProfileDelete).alias("rm"),
			leaf("info", "PROFILEID", "Show a profile", cmdGovProfileInfo),
			leaf("list", "", "List profiles", cmdGovProfileList).alias("ls"),
			leaf("update", "PROFILEID", "Change a profile", cmdGovProfileUpdate)),
		group("ruleset", "Rulesets",
			leaf("classes", "", "List the classes a rule can target", cmdGovRulesetClasses),
			leaf("clone", "RULESET TITLE DESCRIPTION", "Copy a ruleset, moving or removing rules", cmdGovRulesetClone),
			leaf("completions", "RULESET", "Suggest ruleset text at --offset", cmdGovRulesetCompletions),
			leaf("constraints", "", "List the constraints for each kind of property", cmdGovRulesetConstraints),
			leaf("domains", "", "List the model domains", cmdGovRulesetDomains),
			leaf("generate", "INTENT", "Print instructions for writing a ruleset", cmdGovRulesetGenerate),
			leaf("info", "RULESET", "Validate a ruleset and list its rules", cmdGovRulesetInfo),
			leaf("init", "SCHEMA", "Write a ruleset from the types an API spec declares", cmdGovRulesetInit),
			leaf("properties", "TARGETCLASS", "List the properties of a class", cmdGovRulesetProperties),
			leaf("resolve", "TERM", "Find the class and property path for a term", cmdGovRulesetResolve),
			leaf("simplify", "RULESET", "Simplify a ruleset's constraints", cmdGovRulesetSimplify),
			leaf("validate", "RULESET", "Validate a ruleset against the validation profile dialect", cmdGovRulesetValidate),
			leaf("validate-authoring", "RULESET", "Check a ruleset's schema, classes, properties and severities", cmdGovRulesetValidateAuthoring),
			leaf("version", "", "Show the governance tooling versions", cmdGovRulesetVersion)),
		leaf("update", "", "Show the pinned governance tooling", cmdGovUpdate),
	)
}

const assetIdentifierFormat = "ruleset asset identifiers are formatted as <group_id>/<asset_id>/<version>,<group_id>/<asset_id>/<version>"

// rulesetGAVs checks "group/asset/version,..." names existing ruleset
// assets ("latest" allowed) and returns their gav:// URIs.
func rulesetGAVs(x *Ctx, c *anypoint.Client, list string) ([]string, error) {
	var out []string
	for ident := range strings.SplitSeq(list, ",") {
		gav := strings.Split(ident, "/")
		if len(gav) != 3 {
			return nil, fmt.Errorf("invalid ruleset asset identifier %q: %s", ident, assetIdentifierFormat)
		}
		version := gav[2]
		if version == "latest" {
			version = ""
		}
		typ, err := c.AssetType(x.ctx, gav[0], gav[1], version)
		if err != nil {
			return nil, fmt.Errorf("asset does not exist (%s): %w", ident, err)
		}
		if typ != "ruleset" {
			return nil, fmt.Errorf("asset is not ruleset type (%s)", ident)
		}
		out = append(out, anypoint.GAV(gav[0], gav[1], gav[2]))
	}
	return out, nil
}

// notifyConfig follows anypoint-cli-v4: --notify-off disables the current
// configuration; any recipient flag replaces it with an enabled one.
func notifyConfig(off, contact, publisher bool, others string, current *anypoint.NotificationConfig) *anypoint.NotificationConfig {
	if off {
		if current == nil {
			current = &anypoint.NotificationConfig{Notifications: []anypoint.GovernanceNotifyRule{{Condition: "OnFailure", Recipients: []anypoint.GovernanceRecipient{}}}}
		}
		current.Enabled = false
		for i := range current.Notifications {
			current.Notifications[i].Enabled = false
		}
		return current
	}
	if !contact && !publisher && others == "" {
		return current
	}
	rcpt := func(typ, value string) anypoint.GovernanceRecipient {
		return anypoint.GovernanceRecipient{ContactType: typ, NotificationType: "Email", Value: value}
	}
	recipients := []anypoint.GovernanceRecipient{}
	if contact {
		recipients = append(recipients, rcpt("Contact", ""))
	}
	if publisher {
		recipients = append(recipients, rcpt("Publisher", ""))
	}
	if others != "" {
		for mail := range strings.SplitSeq(others, ",") {
			recipients = append(recipients, rcpt("Others", mail))
		}
	}
	return &anypoint.NotificationConfig{Enabled: true, Notifications: []anypoint.GovernanceNotifyRule{{Condition: "OnFailure", Enabled: true, Recipients: recipients}}}
}

func showProfile(x *Ctx, p *anypoint.GovernanceProfile) error {
	return x.Print(p, func() {
		var gavs []string
		for _, r := range p.Rulesets {
			gavs = append(gavs, strings.TrimPrefix(r.URI, "gav://"))
		}
		notify := "Off"
		if n := p.NotificationConfig; n != nil && n.Enabled && len(n.Notifications) > 0 {
			var who []string
			for _, r := range n.Notifications[0].Recipients {
				if r.ContactType == "Others" {
					who = append(who, r.Value)
				} else {
					who = append(who, r.ContactType)
				}
			}
			notify = strings.Join(who, ",")
		}
		printKV(x.Out, "Id", p.ID, "Name", p.Name, "Description", p.Description, "Rulesets", strings.Join(gavs, "\n"),
			"Criteria", p.Filter, "NotificationConfig", notify)
	})
}

type notifyFlags struct {
	off, contact, publisher *bool
	others                  *string
}

func newNotifyFlags(fs *flag.FlagSet, withOff bool) notifyFlags {
	n := notifyFlags{contact: fs.Bool("notify-contact", false, "notify the API's contact on failures"),
		publisher: fs.Bool("notify-publisher", false, "notify the API's publisher on failures"),
		others:    fs.String("notify-others", "", "also notify these `emails`, comma-separated"), off: new(bool)}
	if withOff {
		n.off = fs.Bool("notify-off", false, "turn notifications off")
	}
	return n
}

func cmdGovProfileCreate(x *Ctx, args []string) error {
	fs := x.Flags()
	description := fs.String("description", "", "profile `description`")
	criteria := fs.String("criteria", "", "which APIs the profile governs, as `filterType:value,...` (category, tag, scope), e.g. tag:t1,scope:rest-api")
	n := newNotifyFlags(fs, false)
	pos, c, org, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	gavs, err := rulesetGAVs(x, c, pos[1])
	if err != nil {
		return err
	}
	disabled := &anypoint.NotificationConfig{Notifications: []anypoint.GovernanceNotifyRule{{Condition: "OnFailure", Recipients: []anypoint.GovernanceRecipient{}}}}
	p, err := c.CreateGovernanceProfile(x.ctx, anypoint.GovernanceProfileRequest{Org: org, Name: pos[0], Description: *description,
		Filter: *criteria, Rulesets: gavs, Allowing: []any{}, Denying: []any{},
		NotificationConfig: notifyConfig(false, *n.contact, *n.publisher, *n.others, disabled)})
	if err != nil {
		return fmt.Errorf("create profile failed: %w", err)
	}
	fmt.Fprintln(x.Err, "Profile Added")
	return showProfile(x, p)
}

func cmdGovProfileUpdate(x *Ctx, args []string) error {
	fs := x.Flags()
	rulesets := fs.String("ruleset-asset-identifiers", "", "rulesets as `group/asset/version,...`")
	name := fs.String("profile-name", "", "new `name`")
	criteria := fs.String("criteria", "", "which APIs the profile governs, as `filterType:value,...`")
	description := fs.String("description", "", "new `description`")
	n := newNotifyFlags(fs, true)
	pos, c, org, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	var gavs []string
	if *rulesets != "" {
		if gavs, err = rulesetGAVs(x, c, *rulesets); err != nil {
			return err
		}
	}
	cur, err := c.GetGovernanceProfile(x.ctx, pos[0])
	if err != nil {
		return fmt.Errorf("update failed: %w", err)
	}
	if gavs == nil {
		for _, r := range cur.Rulesets {
			gavs = append(gavs, r.URI)
		}
	}
	err = c.UpdateGovernanceProfile(x.ctx, pos[0], anypoint.GovernanceProfileRequest{ID: pos[0], Org: org,
		Name: cmp(*name, cur.Name), Description: cmp(*description, cur.Description), Filter: cmp(*criteria, cur.Filter),
		Rulesets: gavs, Allowing: cur.Allowing, Denying: cur.Denying,
		NotificationConfig: notifyConfig(*n.off, *n.contact, *n.publisher, *n.others, cur.NotificationConfig)})
	if err != nil {
		return fmt.Errorf("update failed: %w", err)
	}
	return x.Done("Profile updated %s", pos[0])
}

func cmdGovProfileDelete(x *Ctx, args []string) error {
	pos, c, _, err := orgSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := c.DeleteGovernanceProfile(x.ctx, pos[0]); err != nil {
		return fmt.Errorf("delete profile failed: %w", err)
	}
	return x.Done("Profile with id %s removed", pos[0])
}

func cmdGovProfileInfo(x *Ctx, args []string) error {
	pos, c, _, err := orgSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	p, err := c.GetGovernanceProfile(x.ctx, pos[0])
	if err != nil {
		return fmt.Errorf("unexpected error when retrieving profile information: %w", err)
	}
	return showProfile(x, p)
}

func cmdGovProfileList(x *Ctx, args []string) error {
	_, c, org, err := orgSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	profiles, err := c.ListGovernanceProfiles(x.ctx, org)
	if err != nil {
		return fmt.Errorf("unexpected error when listing profiles: %w", err)
	}
	if profiles == nil {
		profiles = []anypoint.GovernanceProfile{}
	}
	return x.Print(profiles, func() {
		if len(profiles) == 0 {
			fmt.Fprintln(x.Out, "No profiles found")
			return
		}
		t := newTable(x.Out, "PROFILE NAME", "PROFILE ID")
		for _, p := range profiles {
			t.row(p.Name, p.ID)
		}
		t.flush()
	})
}

// exchangeDescriptor reads exchange.json from an API project directory or
// zip, or from the file itself.
func exchangeDescriptor(p string) (map[string]any, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("no such file or directory %s", p)
	}
	var data []byte
	switch {
	case st.IsDir():
		data, err = os.ReadFile(filepath.Join(p, "exchange.json"))
	case strings.EqualFold(filepath.Ext(p), ".zip"):
		var zr *zip.ReadCloser
		if zr, err = zip.OpenReader(p); err == nil {
			defer zr.Close()
			err = errors.New("exchange.json not found in the archive")
			for _, f := range zr.File {
				if f.Name == "exchange.json" {
					var rc io.ReadCloser
					if rc, err = f.Open(); err == nil {
						data, err = io.ReadAll(io.LimitReader(rc, 1<<20))
						rc.Close()
					}
				}
			}
		}
	default:
		data, err = os.ReadFile(p)
	}
	if err != nil {
		return nil, fmt.Errorf("error trying to read file or directory %s: %w", p, err)
	}
	var d map[string]any
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("%s: exchange.json: %w", p, err)
	}
	return d, nil
}

func cmdGovEvaluate(x *Ctx, args []string) error {
	fs := x.Flags()
	api := fs.String("api", "", "API project `directory`, zip or exchange.json to evaluate")
	criteria := fs.String("criteria", "", "API `filters`, e.g. tag:t1,category:c:v,scope:rest-api")
	_, c, org, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	if *api == "" && *criteria == "" {
		return errors.New("either --api or --criteria must be provided")
	}
	groupID, assetID := "", ""
	if *api != "" {
		d, err := exchangeDescriptor(*api)
		if err != nil {
			return err
		}
		s := func(k string) string { v, _ := d[k].(string); return v }
		groupID, assetID = s("groupId"), s("assetId")
		org = cmp(s("organizationId"), cmp(groupID, org))
	}
	filter := *criteria
	if !strings.Contains(filter, "scope") {
		scopes := "scope:rest-api,scope:evented-api,scope:async-api"
		if filter != "" {
			filter += ","
		}
		filter += scopes
	}
	uris, err := c.GovernancePlan(x.ctx, org, groupID, assetID, filter)
	if err != nil {
		return fmt.Errorf("unable to obtain information for specified parameters: %w", err)
	}
	gavs := []string{}
	for _, u := range uris {
		if g := strings.TrimPrefix(u, "gav://"); !slices.Contains(gavs, g) {
			gavs = append(gavs, g)
		}
	}
	return x.Print(gavs, func() {
		t := newTable(x.Out, "RULESET GAV")
		for _, g := range gavs {
			t.row(g)
		}
		t.flush()
	})
}
