package service

import (
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

func normalizeQueryBindVariables(rawSQL string) (string, map[string]struct{}, []string, error) {
	var normalized strings.Builder
	names := make(map[string]struct{})
	var ordered []string
	for index := 0; index < len(rawSQL); {
		character := rawSQL[index]
		switch character {
		case '\'':
			end := index + 1
			for end < len(rawSQL) {
				if rawSQL[end] == '\'' {
					if end+1 < len(rawSQL) && rawSQL[end+1] == '\'' {
						end += 2
						continue
					}
					end++
					break
				}
				if rawSQL[end] == '\\' && end+1 < len(rawSQL) {
					end += 2
					continue
				}
				end++
			}
			if end >= len(rawSQL) {
				return "", nil, nil, httperr.BadRequest(40189, "sql_template contains an unterminated string")
			}
			normalized.WriteString(rawSQL[index:end])
			index = end
		case '"', '`':
			end, err := findSQLIdentifierEnd(rawSQL, index)
			if err != nil {
				return "", nil, nil, err
			}
			normalized.WriteString(rawSQL[index:end])
			index = end
		case '-':
			if index+1 < len(rawSQL) && rawSQL[index+1] == '-' {
				end := strings.IndexByte(rawSQL[index:], '\n')
				if end < 0 {
					end = len(rawSQL)
				} else {
					end += index
				}
				normalized.WriteString(rawSQL[index:end])
				index = end
				continue
			}
			normalized.WriteByte(character)
			index++
		case '#':
			end := strings.IndexByte(rawSQL[index:], '\n')
			if end < 0 {
				end = len(rawSQL)
			} else {
				end += index
			}
			normalized.WriteString(rawSQL[index:end])
			index = end
		case '/':
			if index+1 < len(rawSQL) && rawSQL[index+1] == '*' {
				end := strings.Index(rawSQL[index+2:], "*/")
				if end < 0 {
					return "", nil, nil, httperr.BadRequest(40189, "sql_template contains an unterminated comment")
				}
				end += index + 4
				normalized.WriteString(rawSQL[index:end])
				index = end
				continue
			}
			normalized.WriteByte(character)
			index++
		case '?':
			return "", nil, nil, httperr.BadRequest(40196, "sql_template only supports named bind variables")
		case ':':
			if index+1 >= len(rawSQL) || !isQueryBindStart(rawSQL[index+1]) {
				normalized.WriteByte(character)
				index++
				continue
			}
			end := index + 1
			for end < len(rawSQL) && isQueryBindPart(rawSQL[end]) {
				end++
			}
			name := rawSQL[index+1 : end]
			if _, exists := names[name]; !exists {
				names[name] = struct{}{}
				ordered = append(ordered, name)
			}
			normalized.WriteString("?")
			index = end
		default:
			normalized.WriteByte(character)
			index++
		}
	}
	return normalized.String(), names, ordered, nil
}

func findSQLIdentifierEnd(rawSQL string, index int) (int, error) {
	quote := rawSQL[index]
	end := index + 1
	for end < len(rawSQL) {
		if rawSQL[end] != quote {
			end++
			continue
		}
		if end+1 < len(rawSQL) && rawSQL[end+1] == quote {
			end += 2
			continue
		}
		return end + 1, nil
	}
	return 0, httperr.BadRequest(40189, "sql_template contains an unterminated identifier")
}
