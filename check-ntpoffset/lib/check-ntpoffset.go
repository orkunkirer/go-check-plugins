package checkntpoffset

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jessevdk/go-flags"
	"github.com/mackerelio/checkers"
)

var opts struct {
	Crit float64 `short:"c" long:"critical" default:"100" description:"Critical threshold of ntp offset(ms)"`
	Warn float64 `short:"w" long:"warning" default:"50" description:"Warning threshold of ntp offset(ms)"`
}

// Do the plugin
func Do() {
	ckr := run(os.Args[1:])
	ckr.Name = "NTP"
	ckr.Exit()
}

func run(args []string) *checkers.Checker {
	_, err := flags.ParseArgs(&opts, args)
	if err != nil {
		os.Exit(1)
	}

	offset, err := getNTPOffset()
	if err != nil {
		return checkers.Unknown(err.Error())
	}

	var chkSt checkers.Status
	var msg string
	if opts.Crit < math.Abs(offset) {
		msg = fmt.Sprintf("ntp offset is over %f(actual) > %f(threshold)", math.Abs(offset), opts.Crit)
		chkSt = checkers.CRITICAL
	} else if opts.Warn < math.Abs(offset) {
		msg = fmt.Sprintf("ntp offset is over %f(actual) > %f(threshold)", math.Abs(offset), opts.Warn)
		chkSt = checkers.WARNING
	} else {
		msg = fmt.Sprintf("ntp offset is %f(actual) < %f(warning threshold), %f(critial threshold)", math.Abs(offset), opts.Warn, opts.Crit)
		chkSt = checkers.OK
	}

	return checkers.NewChecker(chkSt, msg)
}

func withCmd(cmd *exec.Cmd, fn func(io.Reader) error) error {
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := fn(out); err != nil {
		return err
	}
	return cmd.Wait()
}

const (
	ntpNTPD    = "ntpd"
	ntpChronyd = "chronyd"

	cmdNTPq    = "ntpq"
	cmdChronyc = "chronyc"
)

func hasCommand(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

func detectNTPDname() (ntpdName string, err error) {
	err = withCmd(exec.Command("ps", "-eo", "comm"), func(out io.Reader) error {
		scr := bufio.NewScanner(out)
		for scr.Scan() {
			switch filepath.Base(scr.Text()) {
			case ntpChronyd:
				if hasCommand(cmdChronyc) {
					ntpdName = ntpChronyd
					return nil
				}
			case ntpNTPD:
				if hasCommand(cmdNTPq) {
					ntpdName = ntpNTPD
					return nil
				}
			}
		}
		return fmt.Errorf("no ntp daemons detected")
	})
	return ntpdName, err
}

func getNTPOffset() (float64, error) {
	ntpdName, err := detectNTPDname()
	if err != nil {
		return 0.0, err
	}
	switch ntpdName {
	case ntpNTPD:
		return getNTPOffsetFromNTPD()
	case ntpChronyd:
		return getNTPOffsetFromChrony()
	}
	return 0.0, fmt.Errorf("unsupported ntp daemon %q", ntpdName)
}

func getNTPOffsetFromNTPD() (offset float64, err error) {
	err = withCmd(exec.Command(cmdNTPq, "-c", "rv 0 offset"), func(out io.Reader) error {
		output, err := ioutil.ReadAll(out)
		if err != nil {
			return err
		}
		var line string
		lines := strings.Split(string(output), "\n")
		switch len(lines) {
		case 2:
			line = lines[0]
		case 3:
			/* example on ntp 4.2.2p1-18.el5.centos
			   assID=0 status=06f4 leap_none, sync_ntp, 15 events, event_peer/strat_chg,
			   offset=0.180
			*/
			if strings.Index(lines[0], `assID=0`) == 0 {
				line = lines[1]
				break
			}
			fallthrough
		default:
			return fmt.Errorf("couldn't get ntp offset. ntpd process may be down")
		}
		o := strings.Split(string(line), "=")
		if len(o) != 2 {
			return fmt.Errorf("couldn't get ntp offset. ntpd process may be down")
		}
		offset, err = strconv.ParseFloat(strings.Trim(o[1], "\n"), 64)
		return err
	})
	return offset, err
}

func getNTPOffsetFromChrony() (offset float64, err error) {
	err = withCmd(exec.Command(cmdChronyc, "tracking"), func(out io.Reader) error {
		offset, err = parseNTPOffsetFromChrony(out)
		return err
	})
	return offset, err
}

func parseNTPOffsetFromChrony(out io.Reader) (offset float64, err error) {
	scr := bufio.NewScanner(out)
	for scr.Scan() {
		line := scr.Text()
		if strings.HasPrefix(line, "Last offset") {
			flds := strings.Fields(line)
			if len(flds) != 5 {
				return 0.0, fmt.Errorf("failed to get ntp offset")
			}
			offset, err = strconv.ParseFloat(flds[3], 64)
			if err != nil {
				return 0.0, err
			}
			return offset * 1000, nil
		}
	}
	return 0.0, fmt.Errorf("failed to get ntp offset")
}
