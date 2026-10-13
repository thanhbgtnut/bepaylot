package types

import dtypes "github.com/thanhenti/bepaylot/docpipe/types"

// The types of a parsed page, of a document type and of a classified page
// live in the docpipe module (the reusable PDF → pages → documents → OCR
// flow); bepaylot uses them under their old names.
type (
	BBox        = dtypes.BBox
	Quad        = dtypes.Quad
	BlockType   = dtypes.BlockType
	ParsedPage  = dtypes.ParsedPage
	ParsedBlock = dtypes.ParsedBlock
	ParsedLine  = dtypes.ParsedLine
	TextLayer   = dtypes.TextLayer
	TextWord    = dtypes.TextWord
	PDFBookmark = dtypes.PDFBookmark
	ClassLabel  = dtypes.ClassLabel
	PageDocType = dtypes.PageDocType
)

const (
	BlockTitle      = dtypes.BlockTitle
	BlockHeading    = dtypes.BlockHeading
	BlockParagraph  = dtypes.BlockParagraph
	BlockTable      = dtypes.BlockTable
	BlockFormula    = dtypes.BlockFormula
	BlockFigure     = dtypes.BlockFigure
	BlockCaption    = dtypes.BlockCaption
	BlockHeader     = dtypes.BlockHeader
	BlockFooter     = dtypes.BlockFooter
	BlockPageNumber = dtypes.BlockPageNumber
	BlockFootnote   = dtypes.BlockFootnote
	BlockUnknown    = dtypes.BlockUnknown

	// Text sources for pages and lines (§5.8).
	TextSourceOCR       = dtypes.TextSourceOCR
	TextSourceLayer     = dtypes.TextSourceLayer
	TextSourceMerged    = dtypes.TextSourceMerged
	TextSourceLayerOnly = dtypes.TextSourceLayerOnly
	TextSourceVLM       = dtypes.TextSourceVLM

	// Built-in labels (§6.9.4).
	LabelOther   = dtypes.LabelOther
	LabelUnknown = dtypes.LabelUnknown
)

// BBoxFromArray builds a box from [x0,y0,x1,y1]; short input yields a zero box.
func BBoxFromArray(a []float64) BBox { return dtypes.BBoxFromArray(a) }

// QuadFromFlat is the inverse of Flat.
func QuadFromFlat(a []float64) Quad { return dtypes.QuadFromFlat(a) }

// QuadFromBBox builds a rectangle quad.
func QuadFromBBox(b BBox) Quad { return dtypes.QuadFromBBox(b) }
