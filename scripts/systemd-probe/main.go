// systemd-probe is an isolated Phase 0 experiment, not part of the LINE CLI.
package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"time"
)

const credentialName = "line-cli-phase0-synthetic"

type observation struct {
	Requested string      `json:"requested"`
	Outcome   string      `json:"outcome"`
	Verified  *protection `json:"verified_protection,omitempty"`
	Matches   bool        `json:"requested_mode_matches"`
	Negative  bool        `json:"negative_checks_passed"`
}

type report struct {
	Schema       int           `json:"schema"`
	OS           string        `json:"os"`
	Arch         string        `json:"arch"`
	UID          int           `json:"uid"`
	Version      int           `json:"systemd_version,omitempty"`
	BrokerSocket bool          `json:"broker_socket_present"`
	Outcome      string        `json:"outcome"`
	Observations []observation `json:"observations"`
	RebootTested bool          `json:"reboot_tested"`
}

func main() {
	disposable := flag.Bool("disposable", false, "confirm execution in a disposable Linux test environment")
	enroll := flag.String("enroll", "", "create a new synthetic reboot fixture (exclusive file creation)")
	verify := flag.String("verify", "", "verify a previously enrolled synthetic fixture")
	reboot := flag.Bool("expect-reboot", false, "require a different Linux boot ID when verifying")
	denied := flag.Bool("expect-denied", false, "require helper rejection when verifying a readable fixture")
	flag.Parse()
	if !*disposable || flag.NArg() != 0 || (*enroll != "" && *verify != "") || ((*reboot || *denied) && *verify == "") || (*reboot && *denied) {
		fmt.Fprintln(os.Stderr, "Use --disposable only inside a disposable Linux VM; synthetic sealing can initialize its system host secret.")
		os.Exit(2)
	}
	r := report{Schema: 1, OS: runtime.GOOS, Arch: runtime.GOARCH, UID: os.Geteuid(), Observations: []observation{}}
	if runtime.GOOS != "linux" {
		r.Outcome = "unsupported_os"
	} else if os.Geteuid() == 0 {
		r.Outcome = "requires_unprivileged_user"
	} else {
		h := helper{path: "/usr/bin/systemd-creds", timeout: 10 * time.Second}
		if *enroll != "" || *verify != "" {
			if detect(&r, h) {
				boot, err := linuxBootID()
				if err != nil {
					r.Outcome = "boot_id_unavailable"
				} else if *enroll != "" {
					r.Outcome = enrollFixture(h, r.Version, *enroll, boot)
				} else {
					r.Outcome, r.RebootTested = verifyFixture(h, r.Version, *verify, boot, *reboot, *denied)
				}
			}
		} else {
			probe(&r, h)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(r); err != nil {
		os.Exit(1)
	}
	if r.Outcome != "observed" && r.Outcome != "enrolled" && r.Outcome != "fixture_verified" && r.Outcome != "expected_helper_rejection" {
		os.Exit(1)
	}
}

func probe(r *report, h helper) {
	if !detect(r, h) {
		return
	}
	key := make([]byte, 32)
	defer clear(key)
	if _, err := rand.Read(key); err != nil {
		r.Outcome = "random_source_failed"
		return
	}
	r.Outcome = "observed"
	for _, mode := range []string{"auto", "host", "host+tpm2"} {
		o := observe(h, r.Version, mode, key)
		r.Observations = append(r.Observations, o)
		if mode == "auto" && o.Outcome != "verified" {
			r.Outcome = "baseline_failed"
		}
		if o.Outcome == "negative_check_failed" {
			r.Outcome = "negative_check_failed"
		}
	}
}

func detect(r *report, h helper) bool {
	version, err := h.run([]string{"--version"}, nil, 8192)
	if err != nil {
		r.Outcome = "helper_unavailable"
		return false
	}
	defer clear(version)
	match := regexp.MustCompile(`^systemd ([0-9]+)\b`).FindSubmatch(version)
	if len(match) != 2 {
		r.Outcome = "unrecognized_version"
		return false
	}
	r.Version, _ = strconv.Atoi(string(match[1]))
	if r.Version < 256 {
		r.Outcome = "user_scope_unavailable"
		return false
	}
	info, err := os.Stat("/run/systemd/io.systemd.Credentials")
	r.BrokerSocket = err == nil && info.Mode()&os.ModeSocket != 0
	return true
}

func credentialArgs(version int, verb, name string) []string {
	args := []string{"--user", "--name=" + name, "--newline=no"}
	if version >= 259 {
		args = append(args, "--no-ask-password")
		if verb == "decrypt" {
			args = append(args, "--refuse-null")
		}
	}
	return append(args, verb, "-", "-")
}

func observe(h helper, version int, mode string, key []byte) observation {
	o := observation{Requested: mode}
	args := append([]string{"--with-key=" + mode}, credentialArgs(version, "encrypt", credentialName)...)
	blob, err := h.run(args, key, maxEncoded)
	if err != nil {
		o.Outcome = classify(err)
		return o
	}
	defer clear(blob)
	p, err := inspectCredential(blob)
	if err != nil {
		o.Outcome = "envelope_rejected"
		return o
	}
	plain, err := h.run(credentialArgs(version, "decrypt", credentialName), blob, 32)
	defer clear(plain)
	if err != nil || len(plain) != 32 || subtle.ConstantTimeCompare(plain, key) != 1 {
		o.Outcome = "roundtrip_failed"
		return o
	}
	o.Verified = &p
	o.Matches = mode == "auto" || (mode == "host" && p.Scheme == "host-user") ||
		(mode == "host+tpm2" && p.Scheme != "host-user")
	// A nonzero error alone is not enough: timeout/transport failure does not
	// establish cryptographic rejection. Recheck the valid blob afterwards.
	wrong, err := h.run(credentialArgs(version, "decrypt", credentialName+"-wrong"), blob, 32)
	clear(wrong)
	if !errors.Is(err, errHelper) {
		o.Outcome = "negative_check_failed"
		return o
	}
	raw, err := base64.StdEncoding.DecodeString(string(bytes.Join(bytes.Fields(blob), nil)))
	if err != nil || len(raw) == 0 {
		o.Outcome = "negative_check_failed"
		return o
	}
	defer clear(raw)
	raw[len(raw)-1] ^= 1
	tampered := []byte(base64.StdEncoding.EncodeToString(raw))
	defer clear(tampered)
	wrong, err = h.run(credentialArgs(version, "decrypt", credentialName), tampered, 32)
	clear(wrong)
	if !errors.Is(err, errHelper) {
		o.Outcome = "negative_check_failed"
		return o
	}
	plainAgain, err := h.run(credentialArgs(version, "decrypt", credentialName), blob, 32)
	defer clear(plainAgain)
	if err != nil || len(plainAgain) != 32 || subtle.ConstantTimeCompare(plainAgain, key) != 1 {
		o.Outcome = "negative_check_failed"
		return o
	}
	o.Outcome, o.Negative = "verified", true
	return o
}

func classify(err error) string {
	if errors.Is(err, errTimeout) {
		return "helper_timeout"
	}
	if errors.Is(err, errOutput) {
		return "helper_output_limit"
	}
	return "helper_failed"
}
