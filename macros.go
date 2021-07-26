package sqlds

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/pkg/errors"
)

var (
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
	return fmt.Sprintf("\\$__%s(?:\\((.*?)\\))?", name)
}

func interpolate(driver Driver, query *Query) (string, *data.FillMissing, error) {
	macros := driver.Macros()
	rawSQL := query.RawSQL
	fillMissing := query.FillMissing
	for key, macro := range macros {
		rgx, err := regexp.Compile(getMacroRegex(key))
		if err != nil {
			return "", nil, err
		}
		matches := rgx.FindAllStringSubmatch(rawSQL, -1)
		for _, match := range matches {
			if len(match) == 0 {
				// There were no matches for this macro
				continue
			}

			args := []string{}
			if len(match) > 1 {
				// This macro has arguments
				args = trimAll(strings.Split(match[1], ","))
			}

			tempInterpolatedQuery := query.WithSQL(rawSQL)
			res, err := macro(tempInterpolatedQuery, args)
			if err != nil {
				return "", nil, err
			}

			rawSQL = strings.Replace(rawSQL, match[0], res, -1)
			if tempInterpolatedQuery.FillMissing != nil {
				if fillMissing == nil {
					fillMissing = tempInterpolatedQuery.FillMissing
				} else {
					return "", nil, fmt.Errorf("fill mode can only be set once")
				}
			}
		}

	}

	return rawSQL, fillMissing, nil
}
