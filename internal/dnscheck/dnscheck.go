// Package dnscheck compares the DNS records a domain should have with what
// public resolvers actually answer, from this computer.
//
// The API checks DNS too, on its own schedule and from its own network. Doing
// it locally answers the question people actually have right after editing a
// zone at their registrar: "has it gone through yet, as the world sees it?"
package dnscheck

import (
	"context"
	"errors"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Lookup is the part of *net.Resolver the check uses, so tests can fake it.
type Lookup interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
	LookupCNAME(ctx context.Context, host string) (string, error)
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	LookupTXT(ctx context.Context, name string) ([]string, error)
	LookupNS(ctx context.Context, name string) ([]*net.NS, error)
	LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error)
}

// Resolver asks one DNS server directly, bypassing the operating system's
// cache, which would otherwise keep answering with the old value.
func Resolver(server string) Lookup {
	if server == "" || server == "system" {
		return net.DefaultResolver
	}
	if _, _, err := net.SplitHostPort(server); err != nil {
		server = net.JoinHostPort(server, "53")
	}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, network, server)
		},
	}
}

// Record is one record the domain should publish.
type Record struct {
	ID       string
	Type     string
	Name     string // relative to the domain; "@" is the apex
	Value    string
	Priority *int64
}

// Status is the outcome for one record.
type Status string

const (
	OK          Status = "ok"
	Missing     Status = "missing"
	Different   Status = "different"
	Propagating Status = "propagating" // right on some resolvers, not yet on others
	Unchecked   Status = "cannot check"
	Failed      Status = "error"
)

type Result struct {
	Record Record
	FQDN   string
	Status Status
	// Seen is what the resolvers answered, deduplicated.
	Seen   []string
	Detail string
}

// FQDN turns a record name relative to domain into a full name.
func FQDN(name, domain string) string {
	domain = strings.TrimSuffix(strings.ToLower(domain), ".")
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	switch {
	case name == "" || name == "@":
		return domain
	case name == domain || strings.HasSuffix(name, "."+domain):
		return name
	}
	return name + "." + domain
}

// Check resolves every record against every resolver.
func Check(ctx context.Context, domain string, records []Record, resolvers map[string]Lookup) []Result {
	names := make([]string, 0, len(resolvers))
	for n := range resolvers {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]Result, 0, len(records))
	for _, rec := range records {
		res := Result{Record: rec, FQDN: FQDN(rec.Name, domain)}
		seen := map[string]bool{}
		var oks, founds, errs int
		var lastErr error
		checkable := true
		for _, n := range names {
			answers, err := lookup(ctx, resolvers[n], rec.Type, res.FQDN)
			if errors.Is(err, errUnsupported) {
				checkable = false
				break
			}
			if err != nil && !isNotFound(err) {
				errs++
				lastErr = err
				continue
			}
			if len(answers) > 0 {
				founds++
			}
			for _, a := range answers {
				seen[a] = true
			}
			if matches(rec, answers) {
				oks++
			}
		}
		for s := range seen {
			res.Seen = append(res.Seen, s)
		}
		sort.Strings(res.Seen)
		asked := len(names) - errs
		switch {
		case !checkable:
			res.Status = Unchecked
			res.Detail = rec.Type + " records cannot be looked up from here"
		case asked == 0:
			res.Status = Failed
			if lastErr != nil {
				res.Detail = lastErr.Error()
			}
		case oks == asked:
			res.Status = OK
		case oks > 0:
			res.Status = Propagating
		case founds == 0:
			res.Status = Missing
		default:
			res.Status = Different
		}
		out = append(out, res)
	}
	return out
}

var errUnsupported = errors.New("unsupported record type")

func isNotFound(err error) bool {
	var d *net.DNSError
	return errors.As(err, &d) && d.IsNotFound
}

// lookup returns answers in the same text form the API uses for values.
func lookup(ctx context.Context, r Lookup, typ, name string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	switch strings.ToUpper(typ) {
	case "A", "AAAA":
		ips, err := r.LookupIPAddr(ctx, name)
		if err != nil {
			return nil, err
		}
		var out []string
		for _, ip := range ips {
			v4 := ip.IP.To4() != nil
			if v4 == (strings.ToUpper(typ) == "A") {
				out = append(out, ip.IP.String())
			}
		}
		return out, nil
	case "CNAME":
		c, err := r.LookupCNAME(ctx, name)
		if err != nil {
			return nil, err
		}
		// The resolver reports the name itself when there is no CNAME.
		if normHost(c) == normHost(name) {
			return nil, nil
		}
		return []string{normHost(c)}, nil
	case "MX":
		mxs, err := r.LookupMX(ctx, name)
		if err != nil {
			return nil, err
		}
		var out []string
		for _, mx := range mxs {
			out = append(out, strconv.Itoa(int(mx.Pref))+" "+normHost(mx.Host))
		}
		return out, nil
	case "TXT":
		return r.LookupTXT(ctx, name)
	case "NS":
		nss, err := r.LookupNS(ctx, name)
		if err != nil {
			return nil, err
		}
		var out []string
		for _, ns := range nss {
			out = append(out, normHost(ns.Host))
		}
		return out, nil
	case "SRV":
		_, srvs, err := r.LookupSRV(ctx, "", "", name)
		if err != nil {
			return nil, err
		}
		var out []string
		for _, s := range srvs {
			out = append(out, strconv.Itoa(int(s.Priority))+" "+strconv.Itoa(int(s.Weight))+" "+strconv.Itoa(int(s.Port))+" "+normHost(s.Target))
		}
		return out, nil
	}
	return nil, errUnsupported
}

func normHost(s string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
}

// matches reports whether any answer is the expected value. Values are
// compared the way DNS compares them: host names without case or trailing
// dot, TXT exactly apart from surrounding quotes.
func matches(rec Record, answers []string) bool {
	want := strings.TrimSpace(rec.Value)
	for _, a := range answers {
		switch strings.ToUpper(rec.Type) {
		case "A", "AAAA":
			wip, aip := net.ParseIP(want), net.ParseIP(a)
			if wip != nil && aip != nil && wip.Equal(aip) {
				return true
			}
		case "CNAME", "NS":
			if normHost(want) == a {
				return true
			}
		case "TXT":
			if unquote(want) == a {
				return true
			}
		case "MX":
			pref, host, _ := strings.Cut(a, " ")
			wf := strings.Fields(want)
			switch {
			case len(wf) == 2:
				if wf[0] == pref && normHost(wf[1]) == host {
					return true
				}
			case len(wf) == 1 && normHost(wf[0]) == host:
				if rec.Priority == nil || strconv.FormatInt(*rec.Priority, 10) == pref {
					return true
				}
			}
		case "SRV":
			af := strings.Fields(a)
			wf := strings.Fields(want)
			if len(wf) == 3 && rec.Priority != nil {
				wf = append([]string{strconv.FormatInt(*rec.Priority, 10)}, wf...)
			}
			if len(wf) == 4 && len(af) == 4 && wf[0] == af[0] && wf[1] == af[1] && wf[2] == af[2] && normHost(wf[3]) == af[3] {
				return true
			}
		}
	}
	return false
}

func unquote(s string) string {
	if len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		// Long TXT values are often written as several quoted strings that
		// DNS joins into one.
		parts := strings.Split(s[1:len(s)-1], `" "`)
		return strings.Join(parts, "")
	}
	return s
}
