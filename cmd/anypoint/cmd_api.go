package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/muratcanozdemir/anypoint-go-client/anypoint"
)

// ---------------------------------------------------------------- API Manager

// ---------------------------------------------------------------- raw API

type headerFlag http.Header

func (h headerFlag) String() string { return "" }
func (h headerFlag) Set(s string) error {
	k, v, ok := strings.Cut(s, ":")
	if !ok {
		return fmt.Errorf("want 'Name: value', got %q", s)
	}
	http.Header(h).Add(strings.TrimSpace(k), strings.TrimSpace(v))
	return nil
}

func cmdAPI(x *Ctx, args []string) error {
	fs := x.Flags()
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: anypoint api [flags] PATH

Calls any Anypoint REST endpoint with the current credentials. {org} and {env}
in PATH are replaced by the resolved organization and environment IDs.

Examples:
  anypoint api /accounts/api/me
  anypoint api /cloudhub/api/v2/applications -H 'X-ANYPNT-ENV-ID: {env}'
  anypoint api -X POST -d @team.json /accounts/api/organizations/{org}/teams
  anypoint api /secrets-manager/api/v1/organizations/{org}/environments/{env}/secretGroups

Flags:
`)
		fs.PrintDefaults()
	}
	method := fs.String("X", "", "HTTP `method` (default GET, or POST with -d)")
	data := fs.String("d", "", "request `body`: literal, @file, or @- for stdin")
	include := fs.Bool("i", false, "print response status and headers")
	hdr := headerFlag{}
	fs.Var(hdr, "H", "extra `header` 'Name: value' (repeatable; {org}/{env} expand)")
	pos, err := x.Parse(fs, args)
	if err != nil {
		return err
	}
	c, err := x.Client()
	if err != nil {
		return err
	}
	ctx := x.ctx
	path, err := c.Expand(ctx, pos[0])
	if err != nil {
		return err
	}
	if !strings.HasPrefix(path, "/") && !strings.Contains(path, "://") {
		path = "/" + path
	}
	for k, vs := range hdr {
		for i := range vs {
			if vs[i], err = c.Expand(ctx, vs[i]); err != nil {
				return err
			}
		}
		hdr[k] = vs
	}
	var body []byte
	switch {
	case *data == "@-":
		body, err = io.ReadAll(os.Stdin)
	case strings.HasPrefix(*data, "@"):
		body, err = os.ReadFile((*data)[1:])
	case *data != "":
		body = []byte(*data)
	}
	if err != nil {
		return err
	}
	m := strings.ToUpper(*method)
	if m == "" {
		m = "GET"
		if body != nil {
			m = "POST"
		}
	}
	resp, err := c.Do(ctx, m, path, body, http.Header(hdr))
	if err != nil {
		var he *anypoint.HTTPError
		if errors.As(err, &he) && len(he.Body) > 0 {
			writeBody(x.Out, he.Body)
		}
		return err
	}
	defer resp.Body.Close()
	if *include {
		fmt.Fprintf(x.Out, "%s %s\n", resp.Proto, resp.Status)
		keys := make([]string, 0, len(resp.Header))
		for k := range resp.Header {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(x.Out, "%s: %s\n", k, strings.Join(resp.Header[k], ", "))
		}
		fmt.Fprintln(x.Out)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	writeBody(x.Out, b)
	return nil
}

// writeBody pretty-prints JSON, otherwise writes raw bytes.
func writeBody(w io.Writer, b []byte) {
	var v any
	if json.Unmarshal(b, &v) == nil {
		_ = printJSON(w, v)
		return
	}
	_, _ = w.Write(b)
	if len(b) > 0 && b[len(b)-1] != '\n' {
		fmt.Fprintln(w)
	}
}
