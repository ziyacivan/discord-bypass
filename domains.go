package main

import "strings"

// discordDomains, Discord istemcisinin kullandığı alan adlarıdır. Alt alan
// adları (gateway.discord.gg, cdn.discordapp.com vb.) otomatik eşleşir.
var discordDomains = []string{
	"discord.com",
	"discord.gg",
	"discord.co",
	"discord.app",
	"discord.dev",
	"discord.new",
	"discord.gift",
	"discord.gifts",
	"discord.media",
	"discord.store",
	"discord.tools",
	"discord.design",
	"discordapp.com",
	"discordapp.net",
	"discordcdn.com",
	"discordstatus.com",
	"discordsays.com",
	"discordsez.com",
	"discordactivities.com",
	"discord-activities.com",
	"discordmerch.com",
	"discordpartygames.com",
	"dis.gd",
}

type matcher struct {
	all     bool
	domains []string
}

// newMatcher, Discord listesine kullanıcının virgülle verdiği ek alan
// adlarını ekler. all=true ise proxy'den geçen her alan adı eşleşir.
func newMatcher(extra string, all bool) *matcher {
	m := &matcher{all: all, domains: append([]string(nil), discordDomains...)}
	for _, d := range strings.Split(extra, ",") {
		d = normalizeHost(d)
		if d != "" {
			m.domains = append(m.domains, d)
		}
	}
	return m
}

func (m *matcher) match(host string) bool {
	if m.all {
		return true
	}
	host = normalizeHost(host)
	for _, d := range m.domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}
