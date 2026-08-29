package kit

import (
	"bytes"
	"fmt"

	"github.com/go-pdf/fpdf"
	qrcode "github.com/skip2/go-qrcode"
)

// RenderPDF produces the single-page Recovery Kit (plan §4.3): identity,
// passphrase in large monospace and as a QR code, bucket + read-only
// restore key, the six-word verification code, and handling instructions.
func RenderPDF(d *Data) ([]byte, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}

	pdf := fpdf.New("P", "mm", "Letter", "")
	pdf.SetTitle("Vibe Backup Recovery Kit", false)
	pdf.SetAutoPageBreak(false, 10)
	pdf.AddPage()

	// Header
	pdf.SetFont("Helvetica", "B", 20)
	pdf.CellFormat(0, 10, "Vibe Backup — Recovery Kit", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 11)
	pdf.CellFormat(0, 6, fmt.Sprintf("%s    ·    Appliance %s    ·    Generation %d    ·    Issued %s",
		d.FirmName, d.ApplianceID, d.Generation, d.IssuedDate), "", 1, "L", false, 0, "")
	pdf.Ln(2)
	pdf.SetDrawColor(180, 30, 30)
	pdf.SetLineWidth(0.8)
	x, y := pdf.GetXY()
	pdf.Line(10, y, 206, y)
	pdf.SetXY(x, y+3)

	// Warning box
	pdf.SetFillColor(252, 235, 233)
	pdf.SetFont("Helvetica", "B", 11)
	pdf.MultiCell(0, 6, "THIS PAGE IS THE MASTER KEY TO ALL FIRM AND CLIENT DATA ON YOUR VIBE APPLIANCE.\nAnyone holding it — or a photograph of it — can reconstruct your complete dataset. Store it in a safe or safe-deposit box. Never in a filing cabinet, a shared drive, or a phone. Loss of this page is a security incident: start your incident-response playbook.", "1", "L", true)
	pdf.Ln(4)

	// Passphrase
	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(0, 7, "Repository passphrase", "", 1, "L", false, 0, "")
	pdf.SetFont("Courier", "B", 15)
	pdf.MultiCell(0, 8, splitGroups(d.Passphrase, 16), "1", "C", false)
	pdf.Ln(3)

	// Restore coordinates
	if d.LocalOnly {
		pdf.SetFont("Helvetica", "B", 12)
		pdf.CellFormat(0, 7, "Off-site storage: NONE (local backup only)", "", 1, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 10)
		pdf.MultiCell(0, 5.5, "This firm declined cloud storage. Recovery requires the local backup drive to survive whatever damaged the appliance — fire, theft, and ransomware frequently take both. Reconsider off-site backup.", "", "L", false)
	} else {
		pdf.SetFont("Helvetica", "B", 12)
		pdf.CellFormat(0, 7, "Off-site storage (Backblaze B2, firm-owned account)", "", 1, "L", false, 0, "")
		pdf.SetFont("Courier", "", 11)
		pdf.CellFormat(0, 6, "Bucket:          "+d.B2Bucket, "", 1, "L", false, 0, "")
		if d.B2Prefix != "" {
			pdf.CellFormat(0, 6, "Path:            "+d.B2Prefix, "", 1, "L", false, 0, "")
		}
		pdf.CellFormat(0, 6, "Restore key ID:  "+d.RestoreKeyID, "", 1, "L", false, 0, "")
		pdf.CellFormat(0, 6, "Restore key:     "+d.RestoreKey, "", 1, "L", false, 0, "")
	}
	pdf.Ln(3)

	// QR code, right-aligned block with recovery steps beside it
	png, err := qrcode.Encode(d.QRPayload(), qrcode.Medium, 512)
	if err != nil {
		return nil, fmt.Errorf("kit: qr encode: %w", err)
	}
	opts := fpdf.ImageOptions{ImageType: "PNG"}
	pdf.RegisterImageOptionsReader("kit-qr", opts, bytes.NewReader(png))
	qrY := pdf.GetY()
	pdf.ImageOptions("kit-qr", 150, qrY, 52, 52, false, opts, 0, "")

	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(0, 7, "To recover (dead, encrypted, or missing appliance)", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetRightMargin(75)
	pdf.MultiCell(0, 5.5,
		"1. Get any computer with a fresh Ubuntu install and internet.\n"+
			"2. Run:  curl -fsSL https://get.kisaes.com/restore | sudo bash\n"+
			"3. When prompted, scan the QR code at right — or type the bucket, restore key, and passphrase from this page.\n"+
			"4. Choose the backup date to restore. Everything else is automatic; expect under one hour.\n"+
			"5. Follow the printed re-connection checklist when it finishes.",
		"", "L", false)
	pdf.SetRightMargin(10)
	if pdf.GetY() < qrY+54 {
		pdf.SetY(qrY + 54)
	}
	pdf.Ln(2)

	// Verification code
	pdf.SetFillColor(233, 242, 252)
	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(0, 7, "Activation code — type these six words into the console to start backups", "", 1, "L", false, 0, "")
	pdf.SetFont("Courier", "B", 14)
	pdf.MultiCell(0, 9, d.Verification, "1", "C", true)
	pdf.Ln(3)

	// Custodian block
	pdf.SetFont("Helvetica", "", 10)
	pdf.MultiCell(0, 5.5, "Custodians (two named people, recorded in your WISP as an asset):  this kit's storage location and custodians are recorded in the Vibe console. Re-confirm annually; report loss immediately. This kit becomes obsolete when the next generation's kit is issued — shred THIS page only after the console shows the new generation active.", "", "L", false)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("kit: pdf render: %w", err)
	}
	return buf.Bytes(), nil
}

// splitGroups inserts spaces every n chars and a newline halfway, so the
// 64-char passphrase prints as two readable lines of grouped hex.
func splitGroups(s string, n int) string {
	var out []byte
	for i := 0; i < len(s); i += n {
		end := i + n
		if end > len(s) {
			end = len(s)
		}
		out = append(out, s[i:end]...)
		if end == len(s)/2 {
			out = append(out, '\n')
		} else if end < len(s) {
			out = append(out, ' ')
		}
	}
	return string(out)
}
