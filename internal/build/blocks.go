package build

import (
	"context"
	"html"
	"html/template"

	"github.com/tkjaer/curator/internal/model"
	"github.com/tkjaer/curator/internal/render"
)

// buildBlocks turns a story gallery's blocks into renderable view models. Photos
// are looked up from byItem (already generated in pass 1), so no derivatives are
// produced here.
func (b *Builder) buildBlocks(ctx context.Context, blocks []model.Block, byItem map[int64]render.PhotoView) ([]render.BlockView, error) {
	var out []render.BlockView
	for _, bl := range blocks {
		switch bl.Type {
		case model.BlockHeading:
			out = append(out, render.BlockView{Type: "heading",
				HTML: template.HTML("<h2>" + html.EscapeString(bl.Content) + "</h2>")})
		case model.BlockQuote:
			out = append(out, render.BlockView{Type: "quote",
				HTML: "<blockquote>" + markdownHTML(bl.Content) + "</blockquote>"})
		case model.BlockText:
			out = append(out, render.BlockView{Type: "text", HTML: markdownHTML(bl.Content)})
		case model.BlockImage:
			if bl.ItemID != nil {
				if pv, ok := byItem[*bl.ItemID]; ok {
					photo := pv
					out = append(out, render.BlockView{Type: "image", Photo: &photo})
				}
			}
		case model.BlockGrid:
			ids, err := b.Store.BlockItemIDs(ctx, bl.ID)
			if err != nil {
				return nil, err
			}
			var pics []render.PhotoView
			for _, id := range ids {
				if pv, ok := byItem[id]; ok {
					pics = append(pics, pv)
				}
			}
			rows := render.Justify(pics, contentWidth, optInt(b.options, "rowHeight", 300),
				optInt(b.options, "gridGap", 8), optBool(b.options, "panoFullWidth", true))
			out = append(out, render.BlockView{Type: "grid", Rows: rows})
		}
	}
	return out, nil
}
