package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/muratcanozdemir/anypoint-go-client/anypoint"
)

// anypoint-cli-secrets-mgr-plugin: secrets-mgr:*. Secrets live in a secret
// group of the selected organization and environment.

// secretKind describes one kind of secret: its collection, how its
// create/replace flags turn into a request, and how describe prints it.
type secretKind struct {
	name  string
	kind  anypoint.SecretKind
	short string
	// flags registers the kind's create/replace flags and returns the
	// request builder, called after parsing with --name and --expiration-date.
	flags    func(fs *flag.FlagSet) func(name, expiration string) (any, error)
	describe func(s *anypoint.Secret) []string
	// list columns; target filters TLS contexts.
	tls    bool
	target string
}

func secretsCmd() *Cmd {
	shared := secretKind{name: "shared-secret", kind: anypoint.SharedSecrets, short: "shared secrets", flags: sharedSecretFlags, describe: describeSharedSecret}
	cert := secretKind{name: "certificate", kind: anypoint.Certificates, short: "certificates", flags: certificateFlags, describe: describeCertificate}
	ks := secretKind{name: "keystore", kind: anypoint.Keystores, short: "keystores", flags: keystoreFlags, describe: describeKeystore}
	ts := secretKind{name: "truststore", kind: anypoint.Truststores, short: "truststores", flags: truststoreFlags, describe: describeTruststore}
	mule := secretKind{name: "mule", kind: anypoint.TLSContexts, short: "Mule TLS contexts", flags: muleTLSFlags, describe: describeMuleTLS, tls: true, target: "Mule"}
	flex := secretKind{name: "flex-gateway", kind: anypoint.TLSContexts, short: "Flex Gateway TLS contexts", flags: flexTLSFlags, describe: describeFlexTLS, tls: true, target: "FlexGateway"}
	all := secretKind{kind: anypoint.TLSContexts, tls: true}
	return group("secrets-mgr", "Secrets Manager",
		group("secret-group", "Secret groups",
			leaf("create", "", "Create a secret group", cmdSecretGroupCreate),
			leaf("delete", "", "Delete a secret group", cmdSecretGroupDelete).alias("rm"),
			leaf("describe", "", "Show a secret group", cmdSecretGroupDescribe),
			leaf("list", "", "List secret groups", cmdSecretGroupList).alias("ls"),
			leaf("modify", "", "Rename a secret group or change whether it is downloadable", cmdSecretGroupModify),
		),
		secretKindCmd(shared),
		secretKindCmd(cert),
		secretKindCmd(ks),
		secretKindCmd(ts),
		group("tls-context", "TLS contexts",
			secretKindCmd(flex),
			secretKindCmd(mule),
			leaf("tls-context-list", "", "List all TLS contexts of a secret group", cmdSecretList(all)),
		),
	)
}

func secretKindCmd(k secretKind) *Cmd {
	return group(k.name, strings.ToUpper(k.short[:1])+k.short[1:],
		leaf("create", "", "Create "+article(k.short), cmdSecretCreate(k)),
		leaf("describe", "", "Show one of the "+k.short, cmdSecretDescribe(k)),
		leaf("list", "", "List the "+k.short+" of a secret group", cmdSecretList(k)).alias("ls"),
		leaf("modify", "", "Change the name or expiration date of one of the "+k.short, cmdSecretModify(k)),
		leaf("replace", "", "Replace the content of one of the "+k.short, cmdSecretReplace(k)),
	)
}

func article(plural string) string {
	s := strings.TrimSuffix(plural, "s")
	if strings.ContainsRune("aeiouAEIOU", rune(s[0])) {
		return "an " + s
	}
	return "a " + s
}

// secretSetup parses the flags and resolves the organization and environment.
func secretSetup(x *Ctx, fs *flag.FlagSet, args []string) (c *anypoint.Client, org, env string, err error) {
	if _, c, err = x.Setup(fs, args); err != nil {
		return
	}
	org, env, err = x.OrgEnv(c)
	return
}

func idFlag(fs *flag.FlagSet, what string) *string {
	id := fs.String("id", "", "(required) "+what+" `ID`")
	aliasFlag(fs, "i", "id")
	return id
}

func groupIDFlag(fs *flag.FlagSet) *string {
	g := fs.String("group-id", "", "(required) secret group `ID`")
	aliasFlag(fs, "g", "group-id")
	return g
}

func nameFlag(fs *flag.FlagSet, usage string) *string {
	n := fs.String("name", "", usage)
	aliasFlag(fs, "n", "name")
	return n
}

func printResult(x *Ctx, r *anypoint.SecretResult) error {
	return x.Print(r, func() {
		if r.ID != "" {
			printKV(x.Out, "ID", r.ID, "Message", r.Message)
			return
		}
		printKV(x.Out, "Message", r.Message)
	})
}

// ---------------------------------------------------------------- groups

func cmdSecretGroupList(x *Ctx, args []string) error {
	c, org, env, err := secretSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	groups, err := c.ListSecretGroups(x.ctx, org, env)
	if err != nil {
		return err
	}
	return x.Print(groups, func() {
		t := newTable(x.Out, "ID", "NAME", "DOWNLOADABLE")
		for _, g := range groups {
			t.row(g.Meta.ID, g.Name, fmt.Sprint(g.Downloadable))
		}
		t.flush()
	})
}

func cmdSecretGroupDescribe(x *Ctx, args []string) error {
	fs := x.Flags()
	id := idFlag(fs, "secret group")
	c, org, env, err := secretSetup(x, fs, args)
	if err != nil {
		return err
	}
	if err := need("id", *id); err != nil {
		return err
	}
	g, err := c.GetSecretGroup(x.ctx, org, env, *id)
	if err != nil {
		return err
	}
	return x.Print(g, func() {
		printKV(x.Out, "ID", g.Meta.ID, "Name", g.Name, "Downloadable", fmt.Sprint(g.Downloadable),
			"Created At", g.Meta.CreatedAt, "Modified At", g.Meta.ModifiedAt, "Modified By", g.Meta.ModifiedBy)
	})
}

func cmdSecretGroupCreate(x *Ctx, args []string) error {
	fs := x.Flags()
	name := nameFlag(fs, "(required) secret group `name`")
	downloadable := fs.Bool("downloadable", false, "allow downloading the group's secrets")
	c, org, env, err := secretSetup(x, fs, args)
	if err != nil {
		return err
	}
	if err := need("name", *name); err != nil {
		return err
	}
	r, err := c.CreateSecretGroup(x.ctx, org, env, *name, *downloadable)
	if err != nil {
		return err
	}
	return printResult(x, r)
}

func cmdSecretGroupModify(x *Ctx, args []string) error {
	fs := x.Flags()
	id := idFlag(fs, "secret group")
	name := nameFlag(fs, "new `name`")
	downloadable := fs.Bool("downloadable", false, "make the group downloadable")
	notDownloadable := fs.Bool("no-downloadable", false, "make the group not downloadable")
	c, org, env, err := secretSetup(x, fs, args)
	if err != nil {
		return err
	}
	if err := need("id", *id); err != nil {
		return err
	}
	var dl *bool
	switch {
	case *downloadable && *notDownloadable:
		return errors.New("--downloadable and --no-downloadable are exclusive")
	case *downloadable, *notDownloadable:
		dl = downloadable
	}
	if *name == "" && dl == nil {
		return errors.New("one of the following must be provided when using --id: --name, --downloadable")
	}
	r, err := c.UpdateSecretGroup(x.ctx, org, env, *id, *name, dl)
	if err != nil {
		return err
	}
	return printResult(x, r)
}

func cmdSecretGroupDelete(x *Ctx, args []string) error {
	fs := x.Flags()
	id := idFlag(fs, "secret group")
	c, org, env, err := secretSetup(x, fs, args)
	if err != nil {
		return err
	}
	if err := need("id", *id); err != nil {
		return err
	}
	if err := c.DeleteSecretGroup(x.ctx, org, env, *id); err != nil {
		return err
	}
	return x.Print(map[string]string{"message": "Secret group deleted successfully"}, func() {
		printKV(x.Out, "Message", "Secret group deleted successfully")
	})
}

// ---------------------------------------------------------------- secrets

func cmdSecretList(k secretKind) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		fs := x.Flags()
		groupID := groupIDFlag(fs)
		c, org, env, err := secretSetup(x, fs, args)
		if err != nil {
			return err
		}
		if err := need("group-id", *groupID); err != nil {
			return err
		}
		secrets, err := c.ListSecrets(x.ctx, org, env, *groupID, k.kind, k.target)
		if err != nil {
			return err
		}
		return x.Print(secrets, func() {
			heads := []string{"ID", "NAME", "TYPE", "EXPIRATION DATE"}
			if k.tls {
				heads = []string{"ID", "NAME", "EXPIRATION DATE"}
			}
			t := newTable(x.Out, heads...)
			for _, s := range secrets {
				exp := s.ExpirationDate
				if exp == "" {
					exp = "N/A"
				}
				if k.tls {
					t.row(s.Meta.ID, s.Name, exp)
				} else {
					t.row(s.Meta.ID, s.Name, s.Type, exp)
				}
			}
			t.flush()
		})
	}
}

func cmdSecretDescribe(k secretKind) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		fs := x.Flags()
		id := idFlag(fs, "secret")
		groupID := groupIDFlag(fs)
		c, org, env, err := secretSetup(x, fs, args)
		if err != nil {
			return err
		}
		if err := need("id", *id, "group-id", *groupID); err != nil {
			return err
		}
		s, err := c.GetSecret(x.ctx, org, env, *groupID, k.kind, *id)
		if err != nil {
			return err
		}
		return x.Print(s, func() { printKV(x.Out, k.describe(s)...) })
	}
}

func cmdSecretModify(k secretKind) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		fs := x.Flags()
		id := idFlag(fs, "secret")
		groupID := groupIDFlag(fs)
		name := nameFlag(fs, "new `name`")
		exp := fs.String("expiration-date", "", "new expiration `date`")
		c, org, env, err := secretSetup(x, fs, args)
		if err != nil {
			return err
		}
		if err := need("id", *id, "group-id", *groupID); err != nil {
			return err
		}
		if *name == "" && *exp == "" {
			return errors.New("one of the following must be provided: --name, --expiration-date")
		}
		r, err := c.ModifySecret(x.ctx, org, env, *groupID, k.kind, *id, *name, *exp)
		if err != nil {
			return err
		}
		return printResult(x, r)
	}
}

func cmdSecretCreate(k secretKind) func(*Ctx, []string) error {
	return secretWrite(k, false)
}

func cmdSecretReplace(k secretKind) func(*Ctx, []string) error {
	return secretWrite(k, true)
}

func secretWrite(k secretKind, replace bool) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		fs := x.Flags()
		var id *string
		if replace {
			id = idFlag(fs, "secret")
		}
		groupID := groupIDFlag(fs)
		name := nameFlag(fs, "(required) secret `name`")
		exp := fs.String("expiration-date", "", "expiration `date` of the secret")
		build := k.flags(fs)
		c, org, env, err := secretSetup(x, fs, args)
		if err != nil {
			return err
		}
		if replace {
			if err := need("id", *id); err != nil {
				return err
			}
		}
		if err := need("group-id", *groupID, "name", *name); err != nil {
			return err
		}
		req, err := build(*name, *exp)
		if err != nil {
			return err
		}
		var r *anypoint.SecretResult
		if replace {
			r, err = c.ReplaceSecret(x.ctx, org, env, *groupID, k.kind, *id, req)
		} else {
			r, err = c.CreateSecret(x.ctx, org, env, *groupID, k.kind, req)
		}
		if err != nil {
			return err
		}
		return printResult(x, r)
	}
}

// ---------------------------------------------------------------- kinds

func typeFlag(fs *flag.FlagSet, what string, types ...string) *string {
	t := fs.String("type", "", fmt.Sprintf("(required) %s `type`: %s", what, strings.Join(types, ", ")))
	aliasFlag(fs, "t", "type")
	return t
}

func checkType(t string, types ...string) error {
	if err := need("type", t); err != nil {
		return err
	}
	return oneOf("type", t, types...)
}

// forbid fails if any of the name/value pairs is set.
func forbid(why string, pairs ...string) error {
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			return fmt.Errorf("--%s cannot be used %s", pairs[i], why)
		}
	}
	return nil
}

func sharedSecretFlags(fs *flag.FlagSet) func(string, string) (any, error) {
	types := []string{"Blob", "UsernamePassword", "SymmetricKey", "S3Credential"}
	typ := typeFlag(fs, "shared secret", types...)
	user := fs.String("secret-username", "", "user name (UsernamePassword)")
	pass := fs.String("secret-password", "", "password (UsernamePassword)")
	key := fs.String("key", "", "key value (SymmetricKey)")
	akid := fs.String("access-key-id", "", "S3 access key ID (S3Credential)")
	sak := fs.String("secret-access-key", "", "S3 secret access key (S3Credential)")
	content := fs.String("content", "", "text content (Blob)")
	return func(name, exp string) (any, error) {
		if err := checkType(*typ, types...); err != nil {
			return nil, err
		}
		req := &anypoint.SharedSecretRequest{Type: *typ, Name: name, ExpirationDate: exp}
		others := map[string]string{"secret-username": *user, "secret-password": *pass, "key": *key,
			"access-key-id": *akid, "secret-access-key": *sak, "content": *content}
		var own []string
		switch *typ {
		case "UsernamePassword":
			own = []string{"secret-username", "secret-password"}
			req.Username, req.Password = *user, *pass
		case "SymmetricKey":
			own = []string{"key"}
			req.Key = *key
		case "S3Credential":
			own = []string{"access-key-id", "secret-access-key"}
			req.AccessKeyID, req.SecretAccessKey = *akid, *sak
		case "Blob":
			own = []string{"content"}
			req.Content = *content
		}
		for _, f := range own {
			if err := need(f, others[f]); err != nil {
				return nil, err
			}
			delete(others, f)
		}
		for f, v := range others {
			if err := forbid("with --type "+*typ, f, v); err != nil {
				return nil, err
			}
		}
		return req, nil
	}
}

// storeFile reads a file into a multipart part.
func storeFile(p string) (anypoint.SecretFile, error) {
	b, err := os.ReadFile(expandHome(p))
	if err != nil {
		return anypoint.SecretFile{}, err
	}
	return anypoint.SecretFile{FileName: filepath.Base(p), Content: bytes.NewReader(b)}, nil
}

// storeRequest builds a multipart request from fields (empty values
// skipped) and files (form field -> path, empty skipped).
func storeRequest(fields map[string]string, files map[string]string) (*anypoint.SecretStoreRequest, error) {
	req := &anypoint.SecretStoreRequest{Fields: map[string]string{}, Files: map[string]anypoint.SecretFile{}}
	for k, v := range fields {
		if v != "" {
			req.Fields[k] = v
		}
	}
	for field, p := range files {
		if p == "" {
			continue
		}
		f, err := storeFile(p)
		if err != nil {
			return nil, err
		}
		req.Files[field] = f
	}
	return req, nil
}

var managerAlgorithms = []string{"PKIX", "SunX509"}

func certificateFlags(fs *flag.FlagSet) func(string, string) (any, error) {
	typ := typeFlag(fs, "certificate", "PEM")
	certFile := fs.String("cert-file", "", "(required) certificate `file`")
	return func(name, exp string) (any, error) {
		if err := checkType(*typ, "PEM"); err != nil {
			return nil, err
		}
		if err := need("cert-file", *certFile); err != nil {
			return nil, err
		}
		return storeRequest(map[string]string{"type": *typ, "name": name, "expirationDate": exp},
			map[string]string{"certStore": *certFile})
	}
}

func keystoreFlags(fs *flag.FlagSet) func(string, string) (any, error) {
	types := []string{"PEM", "JKS", "PKCS12", "JCEKS"}
	typ := typeFlag(fs, "keystore", types...)
	ksFile := fs.String("keystore-file", "", "keystore `file` (JKS, PKCS12, JCEKS)")
	keyFile := fs.String("key-file", "", "key `file` (PEM)")
	certFile := fs.String("cert-file", "", "certificate `file` (PEM)")
	capathFile := fs.String("capath-file", "", "CA path certificate `file` (PEM)")
	storePass := fs.String("store-passphrase", "", "keystore passphrase (JKS, PKCS12, JCEKS)")
	keyPass := fs.String("key-passphrase", "", "key passphrase (required for JKS, PKCS12, JCEKS; optional for PEM)")
	alias := fs.String("alias", "", "key alias (JKS, PKCS12, JCEKS)")
	algorithm := fs.String("algorithm", "", "key manager factory `algorithm`: PKIX or SunX509 (JKS, PKCS12, JCEKS)")
	return func(name, exp string) (any, error) {
		if err := checkType(*typ, types...); err != nil {
			return nil, err
		}
		if err := oneOf("algorithm", *algorithm, managerAlgorithms...); err != nil {
			return nil, err
		}
		fields := map[string]string{"type": *typ, "name": name, "keyPassphrase": *keyPass, "expirationDate": exp}
		if *typ == "PEM" {
			if err := need("key-file", *keyFile, "cert-file", *certFile); err != nil {
				return nil, err
			}
			if err := forbid("with a PEM keystore", "alias", *alias, "algorithm", *algorithm, "keystore-file", *ksFile, "store-passphrase", *storePass); err != nil {
				return nil, err
			}
			return storeRequest(fields, map[string]string{"certificate": *certFile, "key": *keyFile, "capath": *capathFile})
		}
		if err := need("keystore-file", *ksFile, "store-passphrase", *storePass, "key-passphrase", *keyPass, "alias", *alias); err != nil {
			return nil, err
		}
		if err := forbid("with a "+*typ+" keystore", "cert-file", *certFile, "capath-file", *capathFile, "key-file", *keyFile); err != nil {
			return nil, err
		}
		fields["storePassphrase"], fields["alias"], fields["algorithm"] = *storePass, *alias, *algorithm
		return storeRequest(fields, map[string]string{"keyStore": *ksFile})
	}
}

func truststoreFlags(fs *flag.FlagSet) func(string, string) (any, error) {
	types := []string{"PEM", "JKS", "PKCS12", "JCEKS"}
	typ := typeFlag(fs, "truststore", types...)
	tsFile := fs.String("truststore-file", "", "(required) truststore `file`")
	storePass := fs.String("store-passphrase", "", "truststore passphrase (JKS, PKCS12, JCEKS)")
	algorithm := fs.String("algorithm", "", "trust manager `algorithm`: PKIX or SunX509 (JKS, PKCS12, JCEKS)")
	return func(name, exp string) (any, error) {
		if err := checkType(*typ, types...); err != nil {
			return nil, err
		}
		if err := need("truststore-file", *tsFile); err != nil {
			return nil, err
		}
		if err := oneOf("algorithm", *algorithm, managerAlgorithms...); err != nil {
			return nil, err
		}
		if *typ == "PEM" {
			if err := forbid("with a PEM truststore", "algorithm", *algorithm, "store-passphrase", *storePass); err != nil {
				return nil, err
			}
		} else if err := need("store-passphrase", *storePass); err != nil {
			return nil, err
		}
		return storeRequest(map[string]string{"type": *typ, "name": name, "storePassphrase": *storePass,
			"algorithm": *algorithm, "expirationDate": exp}, map[string]string{"trustStore": *tsFile})
	}
}

func storeRefs(keystoreID, truststoreID string) (ks, ts *anypoint.SecretRef) {
	if keystoreID != "" {
		ks = &anypoint.SecretRef{Path: "keystores/" + keystoreID}
	}
	if truststoreID != "" {
		ts = &anypoint.SecretRef{Path: "truststores/" + truststoreID}
	}
	return
}

func muleTLSFlags(fs *flag.FlagSet) func(string, string) (any, error) {
	versions := multi(fs, "tls-version", "TLS `version` (repeatable): TLSv1.1 or TLSv1.2 (default TLSv1.2)")
	aliasFlag(fs, "v", "tls-version")
	ksID := fs.String("keystore-id", "", "JKS/JCEKS/PKCS12 keystore `ID` in the group")
	tsID := fs.String("truststore-id", "", "JKS/JCEKS/PKCS12 truststore `ID` in the group")
	insecure := fs.Bool("insecure", false, "disable certificate validation (needs --truststore-id)")
	ciphers := multi(fs, "cipher", "`cipher` for the TLS versions (repeatable; default: the versions' defaults)")
	aliasFlag(fs, "c", "cipher")
	return func(name, exp string) (any, error) {
		if *ksID == "" && *tsID == "" {
			return nil, errors.New("at least one of --keystore-id or --truststore-id is required")
		}
		if *insecure && *tsID == "" {
			return nil, errors.New("--insecure needs --truststore-id")
		}
		vs := []string(*versions)
		if len(vs) == 0 {
			vs = []string{"TLSv1.2"}
		}
		for _, v := range vs {
			if err := oneOf("tls-version", v, "TLSv1.1", "TLSv1.2"); err != nil {
				return nil, err
			}
		}
		req := &anypoint.TLSContextRequest{Target: "Mule", Name: name, ExpirationDate: exp, Insecure: insecure,
			CipherSuites: append([]string{}, *ciphers...),
			AcceptableTLSVersions: &anypoint.AcceptableTLSVersions{
				TLSv1Dot1: slices.Contains(vs, "TLSv1.1"), TLSv1Dot2: slices.Contains(vs, "TLSv1.2"),
			}}
		req.Keystore, req.Truststore = storeRefs(*ksID, *tsID)
		return req, nil
	}
}

// Flex Gateway ciphers and the TLS versions they apply to (1.1 and/or 1.2).
var flexCiphers = map[string][2]bool{
	"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256":       {false, true},
	"TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256": {false, true},
	"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384":       {false, true},
	"TLS_ECDHE_PSK_WITH_CHACHA20_POLY1305_SHA256":   {false, true},
	"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256":         {false, true},
	"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256":   {false, true},
	"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384":         {false, true},
	"TLS_RSA_WITH_AES_128_GCM_SHA256":               {false, true},
	"TLS_RSA_WITH_AES_256_GCM_SHA384":               {false, true},
	"TLS_RSA_WITH_AES_128_CBC_SHA":                  {true, true},
	"TLS_RSA_WITH_AES_256_CBC_SHA":                  {true, true},
	"TLS_PSK_WITH_AES_128_CBC_SHA":                  {true, true},
	"TLS_PSK_WITH_AES_256_CBC_SHA":                  {true, true},
	"TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA":          {true, true},
	"TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA":          {true, true},
	"TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA":            {true, true},
	"TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA":            {true, true},
	"TLS_ECDHE_PSK_WITH_AES_128_CBC_SHA":            {true, true},
	"TLS_ECDHE_PSK_WITH_AES_256_CBC_SHA":            {true, true},
	"TLS_RSA_WITH_NULL_SHA":                         {true, true},
	"TLS_RSA_WITH_3DES_EDE_CBC_SHA":                 {true, false},
}

// flexDefaultCiphers apply when TLS 1.2 is in range and no 1.2 cipher is given.
var flexDefaultCiphers = []string{
	"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256", "TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256",
	"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384", "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
	"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256", "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
}

// flexCipherSuites validates ciphers as anypoint-cli-v4 does and adds the
// TLS 1.2 defaults when needed.
func flexCipherSuites(ciphers []string, minV, maxV string) ([]string, error) {
	var n11, n12 int
	for _, c := range ciphers {
		v, ok := flexCiphers[c]
		if !ok {
			return nil, fmt.Errorf("unacceptable cipher: %s", c)
		}
		if v[0] {
			n11++
		}
		if v[1] {
			n12++
		}
	}
	if minV == "TLSv1.1" && n11 == 0 {
		return nil, errors.New("no cipher specified for the TLS version TLSv1.1")
	}
	out := []string{}
	if (minV == "TLSv1.2" || maxV == "TLSv1.2" || (minV == "TLSv1.1" && maxV == "TLSv1.3")) && n12 == 0 {
		out = append(out, flexDefaultCiphers...)
	}
	return append(out, ciphers...), nil
}

func flexTLSFlags(fs *flag.FlagSet) func(string, string) (any, error) {
	alpn := multi(fs, "alpn-protocol", "ALPN `protocol` (repeatable, in order of preference): h2, http/1.1 (default both)")
	minV := fs.String("min-tls-version", "TLSv1.3", "minimum TLS `version`: TLSv1.1, TLSv1.2 or TLSv1.3")
	maxV := fs.String("max-tls-version", "TLSv1.3", "maximum TLS `version`: TLSv1.1, TLSv1.2 or TLSv1.3")
	ksID := fs.String("keystore-id", "", "PEM keystore `ID` in the group")
	tsID := fs.String("truststore-id", "", "PEM truststore `ID` in the group")
	clientCert := fs.Bool("enable-client-cert-validation", false, "validate client certificates")
	skipServer := fs.Bool("skip-server-cert-validation", false, "skip server certificate validation")
	ciphers := multi(fs, "cipher", "`cipher` for the TLS version range (repeatable)")
	aliasFlag(fs, "c", "cipher")
	return func(name, exp string) (any, error) {
		tls := []string{"TLSv1.1", "TLSv1.2", "TLSv1.3"}
		if err := oneOf("min-tls-version", *minV, tls...); err != nil {
			return nil, err
		}
		if err := oneOf("max-tls-version", *maxV, tls...); err != nil {
			return nil, err
		}
		protocols := []string(*alpn)
		if len(protocols) == 0 {
			protocols = []string{"h2", "http/1.1"}
		}
		for _, p := range protocols {
			if err := oneOf("alpn-protocol", p, "h2", "http/1.1"); err != nil {
				return nil, err
			}
		}
		suites, err := flexCipherSuites(*ciphers, *minV, *maxV)
		if err != nil {
			return nil, err
		}
		req := &anypoint.TLSContextRequest{Target: "FlexGateway", Name: name, ExpirationDate: exp,
			MinTLSVersion: *minV, MaxTLSVersion: *maxV, AlpnProtocols: protocols, CipherSuites: suites,
			InboundSettings:  &anypoint.TLSInboundSettings{EnableClientCertValidation: *clientCert},
			OutboundSettings: &anypoint.TLSOutboundSettings{SkipServerCertValidation: *skipServer}}
		req.Keystore, req.Truststore = storeRefs(*ksID, *tsID)
		return req, nil
	}
}

// ---------------------------------------------------------------- describe

func orNA(s string) string {
	if s == "" {
		return "N/A"
	}
	return s
}

func describeSharedSecret(s *anypoint.Secret) []string {
	kv := []string{"ID", s.Meta.ID, "Name", s.Name, "Type", s.Type}
	if s.Username != "" {
		kv = append(kv, "Username", s.Username)
	}
	if s.AccessKeyID != "" {
		kv = append(kv, "Access Key ID", s.AccessKeyID)
	}
	return append(kv, "Expiration Date", orNA(s.ExpirationDate))
}

func describeCertificate(s *anypoint.Secret) []string {
	return []string{"ID", s.Meta.ID, "Name", s.Name, "Type", s.Type, "Certificate File", s.CertificateFileName,
		"Expiration Date", orNA(s.ExpirationDate)}
}

func describeKeystore(s *anypoint.Secret) []string {
	kv := []string{"ID", s.Meta.ID, "Name", s.Name, "Type", s.Type}
	if s.Type == "PEM" {
		kv = append(kv, "Key File", s.KeyFileName, "Certificate File", s.CertificateFileName)
		if s.CapathFileName != "" {
			kv = append(kv, "CA Path File", s.CapathFileName)
		}
	} else {
		kv = append(kv, "Keystore File", s.KeystoreFileName, "Alias", s.Alias)
		if s.Algorithm != "" {
			kv = append(kv, "Algorithm", s.Algorithm)
		}
	}
	return append(kv, "Expiration Date", orNA(s.ExpirationDate))
}

func describeTruststore(s *anypoint.Secret) []string {
	kv := []string{"ID", s.Meta.ID, "Name", s.Name, "Type", s.Type, "Truststore File", s.TruststoreFileName}
	if s.Algorithm != "" {
		kv = append(kv, "Algorithm", s.Algorithm)
	}
	return append(kv, "Expiration Date", orNA(s.ExpirationDate))
}

func storeIDs(kv []string, s *anypoint.Secret) []string {
	if id := s.SecretKeystoreID(); id != "" {
		kv = append(kv, "Keystore ID", id)
	}
	if id := s.SecretTruststoreID(); id != "" {
		kv = append(kv, "Truststore ID", id)
	}
	return kv
}

func describeMuleTLS(s *anypoint.Secret) []string {
	var versions []string
	if v := s.AcceptableTLSVersions; v != nil {
		if v.TLSv1Dot1 {
			versions = append(versions, "TLSv1.1")
		}
		if v.TLSv1Dot2 {
			versions = append(versions, "TLSv1.2")
		}
	}
	kv := []string{"ID", s.Meta.ID, "Name", s.Name, "TLS Versions", strings.Join(versions, ", "),
		"Ciphers", strings.Join(s.CipherSuites, ", ")}
	kv = storeIDs(kv, s)
	return append(kv, "Insecure", fmt.Sprint(s.Insecure), "Expiration Date", orNA(s.ExpirationDate))
}

func describeFlexTLS(s *anypoint.Secret) []string {
	kv := []string{"ID", s.Meta.ID, "Name", s.Name, "Alpn Protocols", strings.Join(s.AlpnProtocols, ", "),
		"Min TLS Version", s.MinTLSVersion, "Max TLS Version", s.MaxTLSVersion}
	if len(s.CipherSuites) > 0 {
		kv = append(kv, "Ciphers", strings.Join(s.CipherSuites, ", "))
	}
	kv = storeIDs(kv, s)
	if s.InboundSettings != nil {
		kv = append(kv, "Client Cert Validation", fmt.Sprint(s.InboundSettings.EnableClientCertValidation))
	}
	if s.OutboundSettings != nil {
		kv = append(kv, "Skip Server Cert Validation", fmt.Sprint(s.OutboundSettings.SkipServerCertValidation))
	}
	return append(kv, "Expiration Date", orNA(s.ExpirationDate))
}
