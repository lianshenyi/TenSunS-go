// Command verify-user-info checks the authenticated Harbor Mark identity contract.
package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/lianshenyi/TenSunS-go/internal/apicheck"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) != 2 {
		log.Fatal("usage: verify-user-info http://host:port")
	}

	client := apicheck.New(os.Args[1])
	// Read from the controlling terminal with echo disabled. The password
	// crosses an anonymous pipe, leaving command arguments and history empty.
	prompt := exec.Command("bash", "--noprofile", "--norc", "-pc",
		`IFS= read -r -s -p 'Authorized admin password: ' password </dev/tty || exit 1
printf '\n' >/dev/tty
printf '%s' "$password"`)
	prompt.Stderr = os.Stderr
	password, err := prompt.Output()
	if err != nil || client.Login(string(password)) != nil {
		log.Fatal("FAIL: authenticated user-info contract could not be verified")
	}
	if err := client.CheckUserInfo(true); err != nil {
		log.Fatalf("FAIL: %v", err)
	}
	fmt.Println("PASS: authenticated /api/user/info returns the harbor-mark avatar contract")
}
