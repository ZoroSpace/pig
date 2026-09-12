CJK segmentation dictionaries, vendored from github.com/go-ego/gse v1.0.2
(data/dict/zh/s_1.txt and data/dict/zh/t_1.txt, unmodified).

gse is Apache-2.0 (https://github.com/go-ego/gse/blob/master/LICENSE); the
dictionary data originates from the jieba project (MIT) and related corpus
sources, redistributed under gse's packaging.

They are embedded by tui/cjk_segmenter.go via //go:embed and loaded with
gse.Segmenter.LoadDictStr (the same code path as gse's own LoadDictEmbed
"zh" mode). Vendoring avoids linking gse's package-level go:embed blobs
(22.6MB Japanese dictionary + 5.9MB IDF data), which the Go linker keeps in
the binary even when unreferenced.
