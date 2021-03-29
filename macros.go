package sqlds

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/pkg/errors"
)

var (
	// ErrorNoArguments is returned when a macro is used but there were no arguments
	// TODO: Note that it's possible that not every macro has an argument, and we should maybe adjust for this case
	ErrorNoArguments = errors.New("there were no arguments provided to the macro")
	// ErrorBadArgumentCount is returned from macros when the wrong number of arguments were provided
	ErrorBadArgumentCount = errors.New("unexpected number of arguments")
)

// MacroFunc defines a signature for applying a query macro
// Query macro implementations are defined by users / consumers of this package
type MacroFunc func(*Query, []string) (string, error)

// Macros is a list of MacroFuncs.
// The "string" key is the name of the macro function. This name has to be regex friendly.
type Macros map[string]MacroFunc

func trimAll(s []string) []string {
	r := make([]string, len(s))
	for i, v := range s {
		r[i] = strings.TrimSpace(v)
	}

	return r
}

func getMacroRegex(name string) string {
	return fmt.Sprintf("\\$__%s\\((.*?)\\)", name)
}

func interpolate(driver Driver, query *Query) (string, error) {
	macros := driver.Macros()
	rawSQL := query.RawSQL
	for key, macro := range macros {
		rgx, err := regexp.Compile(getMacroRegex(key))
		if err != nil {
			return "", err
		}

		matches := rgx.FindStringSubmatch(query.RawSQL)
		if len(matches) == 0 {
			// There were no matches for this macro
			continue
		}

		if len(matches) == 1 {
			// This macro has no arguments
			return "", ErrorNoArguments
		}

		args := trimAll(strings.Split(matches[1], ","))
		res, err := macro(query.WithSQL(rawSQL), args)
		if err != nil {
			return "", err
		}

		rawSQL = strings.Replace(rawSQL, matches[0], res, -1)
	}

	return rawSQL, nil
}
