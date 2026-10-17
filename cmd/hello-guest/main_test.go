package main

import (
	"net"
	"testing"
)

func TestParseIPs(t *testing.T) {
	got := parseIPs(" 192.168.1.10 ,10.0.0.5,, ")
	want := []string{"192.168.1.10", "10.0.0.5"}
	if len(got) != len(want) {
		t.Fatalf("parseIPs = %v, ждали %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseIPs = %v, ждали %v", got, want)
		}
	}
	if len(parseIPs("")) != 0 {
		t.Fatal("пустая строка должна давать пустой список")
	}
}

func TestChooseIP(t *testing.T) {
	ip := func(s string) net.IP { return net.ParseIP(s) }
	bridge := ipFact{ip("192.168.139.3"), true} // vmware-мост раньше en0 в списке
	lan := ipFact{ip("192.168.35.2"), true}
	vpn := ipFact{ip("10.8.0.2"), false} // utun: без broadcast

	cases := []struct {
		name   string
		route  net.IP
		all    []ipFact
		expect string
	}{
		{"маршрут по умолчанию на lan", ip("192.168.35.2"), []ipFact{bridge, lan, vpn}, "192.168.35.2"},
		{"маршрут через vpn — берём lan", ip("10.8.0.2"), []ipFact{lan, vpn}, "192.168.35.2"},
		{"маршрут публичный — берём частный", ip("8.8.8.8"), []ipFact{bridge, lan}, "192.168.139.3"},
		{"без маршрута — первый частный broadcast", nil, []ipFact{bridge, lan}, "192.168.139.3"},
		{"только vpn-адреса — берём частный", ip("10.8.0.2"), []ipFact{vpn}, "10.8.0.2"},
		{"ничего нет — лупбэк", nil, nil, "127.0.0.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := chooseIP(c.route, c.all).String()
			if got != c.expect {
				t.Fatalf("chooseIP = %s, ждали %s", got, c.expect)
			}
		})
	}
}
