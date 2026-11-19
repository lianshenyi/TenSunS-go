// Command smoke checks the Appendix B compatibility contract against TenSunS.
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/lianshenyi/TenSunS-go/internal/apicheck"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 || len(os.Args) > 3 {
		log.Fatal("usage: smoke http://host:port [admin_password] (defaults to SMOKE_PASSWORD)")
	}
	password, ok := os.LookupEnv("SMOKE_PASSWORD")
	if len(os.Args) == 3 {
		password, ok = os.Args[2], true
	}
	if !ok {
		log.Fatal("FAIL: set SMOKE_PASSWORD or provide an admin password")
	}

	client := apicheck.New(os.Args[1])
	if err := client.Login(password); err != nil {
		log.Fatalf("FAIL: %v", err)
	}
	if err := client.CheckUserInfo(false); err != nil {
		log.Fatalf("FAIL: %v", err)
	}
	if err := client.CheckSmoke(); err != nil {
		log.Fatalf("FAIL: %v", err)
	}
	fmt.Println("PASS: login, info, jobs, edit/cloud, nodes/res, jms/list, jms/config")
}
