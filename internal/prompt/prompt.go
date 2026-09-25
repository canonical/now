// Package prompt builds the messages sent to the model.
package prompt

import (
	"strings"
	"unicode"
)

// Message is one entry of the chat completion request.
type Message struct {
	Role    string
	Content string
}

// BuildOptions carries everything the prompt needs.
type BuildOptions struct {
	// Request is the natural language request.
	Request string

	// Args are the arguments made available to the script via "$@".
	Args []string

	// With are external command names allowed in the script.
	With []string
}

// systemPrompt is the template for the system message. The fence constant
// sidesteps the fact that a raw string cannot contain backticks.
const fence = "```"

const systemPrompt = `
You generate one-shot shell scripts for busybox ash.

## RULES

1. If you cannot satisfy the request, output ONLY an ERROR line, as in this example:
` + fence + `
ERROR cannot move a file to itself: /file/path
` + fence + `
2. If you can satisfy the request, output ONLY a SCRIPT line, and then the script itself, no chat, no code block:
` + fence + `
SCRIPT
echo 'Hello world'
` + fence + `
3. You have ONE chance: completely solve the request with SCRIPT, or fail with ERROR.
4. Use ONLY the explicitly allowed command line tools.
5. Solve the task in a simple, clean, readable way, what an experienced professional would do.
`

const busyboxBuiltins = `
acpid adjtimex ar arch arp arping ascii ash awk base64 basename bc blkdiscard blockdev brctl bunzip2 busybox bzcat bzip2 cal cat chattr chgrp chmod chown chpasswd chroot chvt clear cmp
cp cpio crc32 crond crontab cttyhack cut date dc dd deallocvt depmod devmem df diff dirname dmesg dnsdomainname dos2unix dpkg dpkg-deb du dumpkmap dumpleases echo ed egrep env expand expr
factor fallocate false fatattr fdisk fgrep find findfs fold free freeramdisk fsfreeze fstrim ftpget ftpput getfattr getopt getty grep groups gunzip gzip halt head hexdump hostid hostname httpd
hwclock i2cdetect i2cdump i2cget i2cset i2ctransfer id ifconfig ifdown ifup init insmod install ionice ip ipcalc kill killall klogd last less link linux32 linux64 linuxrc ln loadfont loadkmap
logger login logname logread losetup ls lsattr lsmod lsscsi lzcat lzma lzop md5sum mdev microcom mim mkdir mkdosfs mke2fs mkfifo mknod mkpasswd mkswap mktemp modinfo modprobe more mount mt mv
nameif nbd-client nc netstat nl nologin nproc nsenter nslookup nuke od openvt partprobe passwd paste patch pidof ping ping6 pivot_root poweroff printf ps pwd rdate readlink realpath reboot
renice reset resume rev rm rmdir rmmod route rpm rpm2cpio run-init run-parts sed seq setkeycodes setpriv setsid sh sha1sum sha256sum sha3sum sha512sum shred shuf sleep sort ssl_client
start-stop-daemon stat static-sh strings stty su sulogin svc svok swapoff swapon switch_root sync sysctl syslogd tac tail tar taskset tc tee telnet telnetd test tftp time timeout top touch tr
traceroute traceroute6 true truncate ts tty tunctl ubirename udhcpc udhcpc6 udhcpd uevent umount uname uncompress unexpand uniq unix2dos unlink unlzma unshare unxz unzip uptime usleep uudecode
uuencode vconfig vi w watch watchdog wc wget which who whoami xargs xxd xz xzcat yes zcat zcmp zdiff zegrep zfgrep zforce zgrep zless zmore znew
`

// Build creates the system and user messages for the given options.
func Build(opts BuildOptions) []Message {
	var system strings.Builder
	system.WriteString(systemPrompt)
	system.WriteString("\n## ALLOWED COMMANDS\n")
	system.WriteString(strings.Join(opts.With, " "))
	system.WriteString(busyboxBuiltins)

	var user strings.Builder
	user.WriteString("## REQUEST\n")
	user.WriteString(opts.Request)
	if len(opts.Args) > 0 {
		user.WriteString("\n\n## REQUEST DATA\n```\n")
		for _, arg := range opts.Args {
			user.WriteString(sanitizeArg(arg))
			user.WriteString("\n")
		}
		user.WriteString("```\n" +
			"The first character of each line is not part of the data: `=` means the line is precise, and ! means unprintable characters were replaced by `?` inside that line.\n" +
			"These lines may be accessed by the script in \"$@\" or as literal strings, whichever makes the script simple and clear.\n")
	}

	return []Message{
		{Role: "system", Content: system.String()},
		{Role: "user", Content: user.String()},
	}
}

// sanitizeArg renders one argument as a REQUEST DATA line, prefixed with `=`
// when the text is precise, or `!` when unprintable characters had to be
// replaced by `?`.
func sanitizeArg(arg string) string {
	if isPrintable(arg) {
		return "=" + arg
	}
	var b strings.Builder
	b.WriteString("!")
	for _, r := range arg {
		if r == '\n' || !unicode.IsPrint(r) {
			b.WriteString("?")
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isPrintable(arg string) bool {
	for _, r := range arg {
		if r == '\n' || !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}