package admin

import (
	"encoding/csv"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// writeCSV serializes rows as CSV with the given headers, sets
// Content-Disposition, and writes the response. It writes a UTF-8 BOM so
// Excel/Microsoft tools interpret the file correctly.
func writeCSV(c *gin.Context, filename string, headers []string, rows [][]string) {
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	c.Header("Content-Type", "text/csv; charset=utf-8")
	if _, err := c.Writer.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil { // UTF-8 BOM
		c.Status(http.StatusInternalServerError)
		return
	}

	w := csv.NewWriter(c.Writer)
	_ = w.Write(headers)
	for _, row := range rows {
		_ = w.Write(row)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		c.Status(http.StatusInternalServerError)
	}
}
