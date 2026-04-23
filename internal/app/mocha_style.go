package app

import "github.com/charmbracelet/glamour/ansi"

// Catppuccin Mocha palette — used by the markdown renderer and by lipgloss
// styles. Hex values are the canonical ones from catppuccin.com.
const (
	mochaRosewater = "#f5e0dc"
	mochaFlamingo  = "#f2cdcd"
	mochaPink      = "#f5c2e7"
	mochaMauve     = "#cba6f7"
	mochaRed       = "#f38ba8" // softer red — replaces the aggressive "203"
	mochaMaroon    = "#eba0ac"
	mochaPeach     = "#fab387"
	mochaYellow    = "#f9e2af"
	mochaGreen     = "#a6e3a1"
	mochaTeal      = "#94e2d5"
	mochaSky       = "#89dceb"
	mochaSapphire  = "#74c7ec"
	mochaBlue      = "#89b4fa"
	mochaLavender  = "#b4befe"
	mochaText      = "#cdd6f4"
	mochaSubtext1  = "#bac2de"
	mochaSubtext0  = "#a6adc8"
	mochaOverlay2  = "#9399b2"
	mochaOverlay1  = "#7f849c"
	mochaOverlay0  = "#6c7086"
	mochaSurface2  = "#585b70"
	mochaSurface1  = "#45475a"
	mochaSurface0  = "#313244"
	mochaBase      = "#1e1e2e"
	mochaMantle    = "#181825"
	mochaCrust     = "#11111b"
)

func mochaStr(s string) *string { return &s }
func mochaBool(b bool) *bool    { return &b }
func mochaUint(u uint) *uint    { return &u }

// mochaStyleConfig is a Catppuccin Mocha themed glamour style. It replaces
// the default "dark" style whose inline-code red (ANSI 203) reads too
// aggressively; Mocha swaps in a desaturated pink-red and pulls the rest
// of the palette into line with the surrounding TUI chrome.
var mochaStyleConfig = ansi.StyleConfig{
	Document: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			BlockPrefix: "\n",
			BlockSuffix: "\n",
			Color:       mochaStr(mochaText),
		},
		Margin: mochaUint(2),
	},
	BlockQuote: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Color:  mochaStr(mochaOverlay2),
			Italic: mochaBool(true),
		},
		Indent:      mochaUint(1),
		IndentToken: mochaStr("│ "),
	},
	List: ansi.StyleList{
		LevelIndent: 2,
		StyleBlock: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color: mochaStr(mochaText),
			},
		},
	},
	Heading: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			BlockSuffix: "\n",
			Color:       mochaStr(mochaMauve),
			Bold:        mochaBool(true),
		},
	},
	H1: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix:          " ",
			Suffix:          " ",
			Color:           mochaStr(mochaBase),
			BackgroundColor: mochaStr(mochaMauve),
			Bold:            mochaBool(true),
		},
	},
	H2: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix: "## ",
			Color:  mochaStr(mochaLavender),
			Bold:   mochaBool(true),
		},
	},
	H3: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix: "### ",
			Color:  mochaStr(mochaBlue),
			Bold:   mochaBool(true),
		},
	},
	H4: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix: "#### ",
			Color:  mochaStr(mochaSapphire),
		},
	},
	H5: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix: "##### ",
			Color:  mochaStr(mochaSky),
		},
	},
	H6: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix: "###### ",
			Color:  mochaStr(mochaTeal),
		},
	},
	Strikethrough: ansi.StylePrimitive{
		CrossedOut: mochaBool(true),
	},
	Emph: ansi.StylePrimitive{
		Color:  mochaStr(mochaYellow),
		Italic: mochaBool(true),
	},
	Strong: ansi.StylePrimitive{
		Color: mochaStr(mochaPeach),
		Bold:  mochaBool(true),
	},
	HorizontalRule: ansi.StylePrimitive{
		Color:  mochaStr(mochaSurface2),
		Format: "\n--------\n",
	},
	Item: ansi.StylePrimitive{
		BlockPrefix: "• ",
	},
	Enumeration: ansi.StylePrimitive{
		BlockPrefix: ". ",
		Color:       mochaStr(mochaSky),
	},
	Task: ansi.StyleTask{
		StylePrimitive: ansi.StylePrimitive{},
		Ticked:         "[✓] ",
		Unticked:       "[ ] ",
	},
	Link: ansi.StylePrimitive{
		Color:     mochaStr(mochaSapphire),
		Underline: mochaBool(true),
	},
	LinkText: ansi.StylePrimitive{
		Color: mochaStr(mochaBlue),
		Bold:  mochaBool(true),
	},
	Image: ansi.StylePrimitive{
		Color:     mochaStr(mochaSapphire),
		Underline: mochaBool(true),
	},
	ImageText: ansi.StylePrimitive{
		Color:  mochaStr(mochaPink),
		Format: "Image: {{.text}} →",
	},
	Code: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix:          " ",
			Suffix:          " ",
			Color:           mochaStr(mochaFlamingo),
			BackgroundColor: mochaStr(mochaSurface0),
		},
	},
	CodeBlock: ansi.StyleCodeBlock{
		StyleBlock: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color: mochaStr(mochaSubtext1),
			},
			Margin: mochaUint(2),
		},
		Chroma: &ansi.Chroma{
			Text: ansi.StylePrimitive{
				Color: mochaStr(mochaText),
			},
			Error: ansi.StylePrimitive{
				Color:           mochaStr(mochaBase),
				BackgroundColor: mochaStr(mochaRed),
			},
			Comment: ansi.StylePrimitive{
				Color:  mochaStr(mochaOverlay0),
				Italic: mochaBool(true),
			},
			CommentPreproc: ansi.StylePrimitive{
				Color: mochaStr(mochaPink),
			},
			Keyword: ansi.StylePrimitive{
				Color: mochaStr(mochaMauve),
			},
			KeywordReserved: ansi.StylePrimitive{
				Color: mochaStr(mochaMauve),
			},
			KeywordNamespace: ansi.StylePrimitive{
				Color: mochaStr(mochaPink),
			},
			KeywordType: ansi.StylePrimitive{
				Color: mochaStr(mochaYellow),
			},
			Operator: ansi.StylePrimitive{
				Color: mochaStr(mochaSky),
			},
			Punctuation: ansi.StylePrimitive{
				Color: mochaStr(mochaOverlay2),
			},
			Name: ansi.StylePrimitive{
				Color: mochaStr(mochaText),
			},
			NameBuiltin: ansi.StylePrimitive{
				Color: mochaStr(mochaRed),
			},
			NameTag: ansi.StylePrimitive{
				Color: mochaStr(mochaMauve),
			},
			NameAttribute: ansi.StylePrimitive{
				Color: mochaStr(mochaYellow),
			},
			NameClass: ansi.StylePrimitive{
				Color: mochaStr(mochaYellow),
				Bold:  mochaBool(true),
			},
			NameConstant: ansi.StylePrimitive{
				Color: mochaStr(mochaPeach),
			},
			NameDecorator: ansi.StylePrimitive{
				Color: mochaStr(mochaBlue),
			},
			NameFunction: ansi.StylePrimitive{
				Color: mochaStr(mochaBlue),
			},
			LiteralNumber: ansi.StylePrimitive{
				Color: mochaStr(mochaPeach),
			},
			LiteralString: ansi.StylePrimitive{
				Color: mochaStr(mochaGreen),
			},
			LiteralStringEscape: ansi.StylePrimitive{
				Color: mochaStr(mochaPink),
			},
			GenericDeleted: ansi.StylePrimitive{
				Color: mochaStr(mochaRed),
			},
			GenericEmph: ansi.StylePrimitive{
				Italic: mochaBool(true),
			},
			GenericInserted: ansi.StylePrimitive{
				Color: mochaStr(mochaGreen),
			},
			GenericStrong: ansi.StylePrimitive{
				Bold: mochaBool(true),
			},
			GenericSubheading: ansi.StylePrimitive{
				Color: mochaStr(mochaOverlay1),
			},
			Background: ansi.StylePrimitive{
				BackgroundColor: mochaStr(mochaMantle),
			},
		},
	},
	Table: ansi.StyleTable{
		StyleBlock: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{},
		},
		CenterSeparator: mochaStr("┼"),
		ColumnSeparator: mochaStr("│"),
		RowSeparator:    mochaStr("─"),
	},
	DefinitionDescription: ansi.StylePrimitive{
		BlockPrefix: "\n🠶 ",
	},
}
