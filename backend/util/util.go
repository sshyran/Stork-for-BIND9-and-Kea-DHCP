package storkutil

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"golang.org/x/term"
)

func UTCNow() time.Time {
	return time.Now().UTC()
}

// Returns URL of the host with port.
func HostWithPortURL(address string, port int64, secure bool) string {
	protocol := "http"
	if secure {
		protocol = "https"
	}
	return fmt.Sprintf("%s://%s:%d/", protocol, address, port)
}

// Parses URL into host and port.
func ParseURL(url string) (host string, port int64, secure bool) {
	ptrn := regexp.MustCompile(`https{0,1}:\/\/\[{1}(\S+)\]{1}(:([0-9]+)){0,1}`)
	m := ptrn.FindStringSubmatch(url)

	if len(m) == 0 {
		ptrn := regexp.MustCompile(`https{0,1}:\/\/([^\s\:\/]+)(:([0-9]+)){0,1}`)
		m = ptrn.FindStringSubmatch(url)
	}

	if len(m) > 1 {
		host = m[1]
	}

	if len(m) > 3 {
		p, err := strconv.Atoi(m[3])
		if err == nil {
			port = int64(p)
		}
	}

	secure = strings.HasPrefix(url, "https://")

	// Set default ports
	if port == 0 {
		switch {
		case strings.HasPrefix(url, "http://"):
			port = 80
		case strings.HasPrefix(url, "https://"):
			port = 443
		}
	}

	return host, port, secure
}

// Formats provided string of hexadecimal digits to MAC address format
// using colon as separator. It returns formatted string and a boolean
// value indicating if the conversion was successful.
func FormatMACAddress(identifier string) (formatted string, ok bool) {
	// Check if the identifier is already in the desired format.
	identifier = strings.TrimSpace(identifier)
	pattern := regexp.MustCompile(`^[0-9A-Fa-f]{2}((:{1})[0-9A-Fa-f]{2})*$`)
	if pattern.MatchString(identifier) {
		// No conversion required. Return the input.
		return identifier, true
	}
	// We will have to convert it, but let's first check if this is a valid identifier.
	if !IsHexIdentifier(identifier) {
		return "", false
	}
	// Remove any colons and whitespaces.
	replacer := strings.NewReplacer(" ", "", ":", "")
	numericOnly := replacer.Replace(identifier)
	for i, character := range numericOnly {
		formatted += string(character)
		// Divide the string into groups with two digits.
		if i > 0 && i%2 != 0 && i < len(numericOnly)-1 {
			formatted += ":"
		}
	}
	return formatted, true
}

// Detects if the provided string is an identifier consisting of
// hexadecimal digits and optionally whitespace or colons between
// the groups of digits. For example: 010203, 01:02:03, 01::02::03,
// 01 02 03 etc. It is useful in detecting if the string comprises
// a DHCP client identifier or MAC address.
func IsHexIdentifier(text string) bool {
	pattern := regexp.MustCompile(`^[0-9A-Fa-f]{2}((\s*|:{0,2})[0-9A-Fa-f]{2})*$`)
	return pattern.MatchString(strings.TrimSpace(text))
}

func SetupLogging() {
	log.SetLevel(log.InfoLevel)
	log.SetOutput(os.Stdout)
	log.SetReportCaller(true)
	log.SetFormatter(&log.TextFormatter{
		ForceColors:     true,
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
		// TODO: do more research and enable if it brings value
		// PadLevelText: true,
		// FieldMap: log.FieldMap{
		// 	FieldKeyTime:  "@timestamp",
		// 	FieldKeyLevel: "@level",
		// 	FieldKeyMsg:   "@message",
		// },
		CallerPrettyfier: func(f *runtime.Frame) (string, string) {
			// Grab filename and line of current frame and add it to log entry
			_, filename := path.Split(f.File)
			return "", fmt.Sprintf("%20v:%-5d", filename, f.Line)
		},
	})
}

// Helper code for mocking os/exec stuff... pathetic.
type Commander interface {
	Output(string, ...string) ([]byte, error)
}

type RealCommander struct{}

func (c RealCommander) Output(command string, args ...string) ([]byte, error) {
	return exec.Command(command, args...).Output()
}

// Convert bytes to hex string.
func BytesToHex(bytesArray []byte) string {
	var buf bytes.Buffer
	for _, f := range bytesArray {
		fmt.Fprintf(&buf, "%02X", f)
	}
	return buf.String()
}

// Convert a string of hexadecimal digits to bytes array.
func HexToBytes(hexString string) []byte {
	hexString = strings.ReplaceAll(hexString, ":", "")
	decoded, _ := hex.DecodeString(hexString)
	return decoded
}

func GetSecretInTerminal(prompt string) string {
	// Prompt the user for a secret
	fmt.Print(prompt)
	pass, err := term.ReadPassword(0)
	fmt.Print("\n")

	if err != nil {
		log.Fatal(err.Error())
	}
	return string(pass)
}

// Read a file and resolve all include statements.
func ReadFileWithIncludes(path string) (string, error) {
	parentPaths := map[string]bool{}
	return readFileWithIncludes(path, parentPaths)
}

// Recursive function to read a file and resolve all include statements.
func readFileWithIncludes(path string, parentPaths map[string]bool) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		log.Warnf("cannot read file: %+v", err)
		err = errors.Wrap(err, "cannot read file")
		return "", err
	}

	text := string(raw)

	// Include pattern definition:
	// - Must start with prefix: <?include
	// - Must end with suffix: ?>
	// - Path may be relative to parent file or absolute
	// - Path must be escaped with double quotes
	// - May to contains spacing before and after the path quotes
	// - Path must contain ".json" extension
	// Produce two groups: first for the whole statement and second for path.
	includePattern := regexp.MustCompile(`<\?include\s*\"([^"]+\.json)\"\s*\?>`)
	matchesGroupIndices := includePattern.FindAllStringSubmatchIndex(text, -1)
	matchesGroups := includePattern.FindAllStringSubmatch(text, -1)

	// Probably never met
	if (matchesGroupIndices == nil) != (matchesGroups == nil) {
		return "", errors.New("include statement recognition failed")
	}

	// No matches - nothing to expand
	if matchesGroupIndices == nil {
		return text, nil
	}

	// Probably never met
	if len(matchesGroupIndices) != len(matchesGroups) {
		return "", errors.New("include statement recognition asymmetric")
	}

	// The root directory for includes
	baseDirectory := filepath.Dir(path)

	// Iteration from the end to keep correct index values because when the pattern
	// is replaced with an include content the positions of next patterns are shifting
	for i := len(matchesGroupIndices) - 1; i >= 0; i-- {
		matchedGroupIndex := matchesGroupIndices[i]
		matchedGroup := matchesGroups[i]

		statementStartIndex := matchedGroupIndex[0]
		matchedPath := matchedGroup[1]
		matchedStatementLength := len(matchedGroup[0])
		statementEndIndex := statementStartIndex + matchedStatementLength

		// Include path may be absolute or relative to a parent file
		nestedIncludePath := matchedPath
		if !filepath.IsAbs(nestedIncludePath) {
			nestedIncludePath = filepath.Join(baseDirectory, nestedIncludePath)
		}
		nestedIncludePath = filepath.Clean(nestedIncludePath)

		// Check for infinite loop
		_, isVisited := parentPaths[nestedIncludePath]
		if isVisited {
			err := errors.Errorf("detected infinite loop on include '%s' in file '%s'", matchedPath, path)
			return "", err
		}

		// Prepare the parent paths for the nested level
		nestedParentPaths := make(map[string]bool, len(parentPaths)+1)
		for k, v := range parentPaths {
			nestedParentPaths[k] = v
		}
		nestedParentPaths[nestedIncludePath] = true

		// Recursive call
		content, err := readFileWithIncludes(nestedIncludePath, nestedParentPaths)
		if err != nil {
			return "", errors.Wrapf(err, "problem with inner include: '%s' of '%s': '%s'", matchedPath, path, nestedIncludePath)
		}

		// Replace include statement with included content
		text = text[:statementStartIndex] + content + text[statementEndIndex:]
	}

	return text, nil
}

// Hide any sensitive data in the object. Data is sensitive if its key is equal to "password", "token" or "secret".
func HideSensitiveData(obj *map[string]interface{}) {
	for entryKey, entryValue := range *obj {
		// Check if the value holds sensitive data.
		entryKeyNormalized := strings.ToLower(entryKey)
		if entryKeyNormalized == "password" || entryKeyNormalized == "secret" || entryKeyNormalized == "token" {
			(*obj)[entryKey] = nil
			continue
		}
		// Check if it is an array.
		array, ok := entryValue.([]interface{})
		if ok {
			for _, arrayItemValue := range array {
				// Check if it is a subobject (or array).
				subobject, ok := arrayItemValue.(map[string]interface{})
				if ok {
					HideSensitiveData(&subobject)
				}
			}
			continue
		}
		// Check if it is a subobject (but not array).
		subobject, ok := entryValue.(map[string]interface{})
		if ok {
			HideSensitiveData(&subobject)
		}
	}
}

// Check if the filename has a conventional timestamp prefix.
// Returns a parsed timestamp, rest of filename and error (if failed).
func ParseTimestampPrefix(filename string) (time.Time, string, error) {
	timestampEnd := strings.Index(filename, "_")
	if timestampEnd <= 0 {
		return time.Time{}, "", errors.New("missing prefix delimiter")
	}
	if timestampEnd < len(time.RFC3339)-5 { // Timezone is optional
		return time.Time{}, "", errors.New("timestamp is too short")
	}

	raw := filename[:timestampEnd]
	raw = raw[:11] + strings.ReplaceAll(raw[11:], "-", ":")

	timestamp, err := time.Parse(time.RFC3339, raw)
	return timestamp, filename[len(raw):], err
}

// Check if it is possible to create a file
// with the provided filename.
func IsValidFilename(filename string) bool {
	if strings.ContainsAny(filename, "*") {
		return false
	}
	file, err := os.CreateTemp("", filename+"*")
	if err != nil {
		return false
	}
	file.Close()
	os.Remove(file.Name())
	return true
}

// Returns a string comprising a count and a noun in the plural or
// singular form, depending on the count. The third parameter is a
// postfix making the plural form.
func FormatNoun(count int64, noun, postfix string) string {
	formatted := fmt.Sprintf("%d %s", count, noun)
	if count != 1 && count != -1 {
		formatted += postfix
	}
	return formatted
}
