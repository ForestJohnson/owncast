package main

import (
	"fmt"
	"io/ioutil"
	"net"
	"strings"

	"github.com/hirochachacha/go-smb2"
)

func main() {
	conn, err := net.Dial("tcp", "localhost:445")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	d := &smb2.Dialer{
		Initiator: &smb2.NTLMInitiator{
			User:     "guest",
			Password: "",
		},
	}

	s, err := d.Dial(conn)
	if err != nil {
		panic(err)
	}
	defer s.Logoff()

	shareNames, err := s.ListSharenames()
	toMount := ""
	for _, name := range shareNames {
		if (strings.ToLower(name) == "public" && toMount == "") || strings.ToLower(name) == "owncast" {
			toMount = name
		}
	}

	fs, err := s.Mount(toMount)
	if err != nil {
		panic(err)
	}
	defer fs.Umount()

	f, err := fs.Open("test")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	bs, err := ioutil.ReadAll(f)
	if err != nil {
		panic(err)
	}

	fmt.Println(string(bs))
}
