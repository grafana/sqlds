package sqlds

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
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

// Default time filter for SQL based on the query time range.
// It requires one argument, the time column to filter.
// Example:
//   $__timeFilter(time) => "time BETWEEN '2006-01-02T15:04:05Z07:00' AND '2006-01-02T15:04:05Z07:00'"
func macroTimeFilter(query *Query, args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("%w: expected 1 argument, received %d", ErrorBadArgumentCount, len(args))
	}

	var (
		column = args[0]
		from   = query.TimeRange.From.UTC().Format(time.RFC3339)
		to     = query.TimeRange.To.UTC().Format(time.RFC3339)
	)

	return fmt.Sprintf("%s >= '%s' AND %s <= '%s'", column, from, column, to), nil
}

// Default time filter for SQL based on the starting query time range.
// It requires one argument, the time column to filter.
// Example:
//   $__timeFrom(time) => "time > '2006-01-02T15:04:05Z07:00'"
func macroTimeFrom(query *Query, args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("%w: expected 1 argument, received %d", ErrorBadArgumentCount, len(args))
	}

	return fmt.Sprintf("%s >= '%s'", args[0], query.TimeRange.From.UTC().Format(time.RFC3339)), nil

}

// Default time filter for SQL based on the ending query time range.
// It requires one argument, the time column to filter.
// Example:
//   $__timeTo(time) => "time < '2006-01-02T15:04:05Z07:00'"
func macroTimeTo(query *Query, args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("%w: expected 1 argument, received %d", ErrorBadArgumentCount, len(args))
	}

	return fmt.Sprintf("%s <= '%s'", args[0], query.TimeRange.To.UTC().Format(time.RFC3339)), nil
}

// Default time group for SQL based the given period.
// This basic example is meant to be customized with more complex periods.
// It requires two arguments, the column to filter and the period.
// Example:
//   $__timeTo(time, month) => "datepart(year, time), datepart(month, time)'"
func macroTimeGroup(query *Query, args []string) (string, error) {
	if len(args) != 2 {
		return "", fmt.Errorf("%w: expected 1 argument, received %d", ErrorBadArgumentCount, len(args))
	}

	res := ""
	switch args[1] {
	case "minute":
		res += fmt.Sprintf("datepart(minute, %s),", args[0])
		fallthrough
	case "hour":
		res += fmt.Sprintf("datepart(hour, %s),", args[0])
		fallthrough
	case "day":
		res += fmt.Sprintf("datepart(day, %s),", args[0])
		fallthrough
	case "month":
		res += fmt.Sprintf("datepart(month, %s),", args[0])
		fallthrough
	case "year":
		res += fmt.Sprintf("datepart(year, %s)", args[0])
	}

	return res, nil
}

// Default macro to return the query table name.
// Example:
//   $__table => "my_table"
func macroTable(query *Query, args []string) (string, error) {
	return query.Table, nil
}

// Default macro to return the query column name.
// Example:
//   $__column => "my_col"
func macroColumn(query *Query, args []string) (string, error) {
	return query.Column, nil
}

var DefaultMacros Macros = Macros{
	"timeFilter": macroTimeFilter,
	"timeFrom":   macroTimeFrom,
	"timeGroup":  macroTimeGroup,
	"timeTo":     macroTimeTo,
	"table":      macroTable,
	"column":     macroColumn,
}

func trimAll(s []string) []string {
	r := make([]string, len(s))
	for i, v := range s {
		r[i] = strings.TrimSpace(v)
	}

	return r
}

func getMacroRegex(name string) string {
	return fmt.Sprintf("\\$__%s\\b(?:\\((.*?)\\))?", name)
}

func interpolate(driver Driver, query *Query) (string, error) {
	macros := driver.Macros()
	for key, defaultMacro := range DefaultMacros {
		if _, ok := macros[key]; !ok {
			// If the driver doesn't define some macro, use the default one
			macros[key] = defaultMacro
		}
	}
	rawSQL := query.RawSQL
	for key, macro := range macros {
		rgx, err := regexp.Compile(getMacroRegex(key))
		if err != nil {
			return rawSQL, err
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

			res, err := macro(query.WithSQL(rawSQL), args)
			if err != nil {
				return rawSQL, err
			}

			rawSQL = strings.Replace(rawSQL, match[0], res, -1)
		}

	}

	return rawSQL, nil
}
