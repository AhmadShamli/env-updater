package env

// RecordType represents the type of entry parsed from a .env file.
type RecordType int

const (
	RecordBlank RecordType = iota
	RecordComment
	RecordMalformed
	RecordKey
)

// Record represents a single logical entry in a .env file.
// For quoted values spanning multiple lines, RawLines preserves all original lines byte-for-byte.
type Record struct {
	Type        RecordType
	Key         string
	CleanValue  string
	HasExport   bool
	RawLines    []string
	LineEndings []string
	StartLine   int
}

// EnvFile holds the parsed records and metadata of a .env file.
type EnvFile struct {
	Path        string
	Records     []Record
	UseCRLF     bool
	LineEnding  string
	KeyIndex    map[string]int   // maps Key -> index in Records
	Duplicates  map[int]bool     // set of Record indices that are duplicates
	AttachedDoc map[int][]Record // comments directly preceding this record
}

const UnparsedContentHeader = "# Unparsed content preserved from .env"
