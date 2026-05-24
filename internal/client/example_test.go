package client_test

import (
	"fmt"
	"strings"

	"github.com/jdel/emission/internal/client"
)

func ExampleNew() {
	c, err := client.New("transmission-3.00")
	if err != nil {
		panic(err)
	}
	tmpl, headers := c.Query()
	fmt.Println("template has peer_id placeholder:", strings.Contains(tmpl, "{peerid}"))
	fmt.Println("peer_id starts with -TR3000-:", strings.HasPrefix(c.PeerID, "-TR3000-"))
	for _, h := range headers {
		if h.Name == "User-Agent" {
			fmt.Println("ua:", h.Value)
		}
	}
	// Output:
	// template has peer_id placeholder: true
	// peer_id starts with -TR3000-: true
	// ua: Transmission/3.00
}

func ExampleVersions() {
	versions := client.Versions()
	fmt.Println("count:", len(versions))
	fmt.Println("first:", versions[0])
	// Output:
	// count: 85
	// first: bittorrent-7.10.1_43917
}
