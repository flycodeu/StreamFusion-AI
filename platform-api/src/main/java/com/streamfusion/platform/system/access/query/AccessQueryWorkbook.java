package com.streamfusion.platform.system.access.query;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import java.io.ByteArrayOutputStream;
import java.io.FilterOutputStream;
import java.io.IOException;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.stream.Collectors;
import java.util.zip.ZipEntry;
import java.util.zip.ZipOutputStream;

/** Small, text-only XLSX writer; no formulas, external links, macros or production dependency. */
final class AccessQueryWorkbook {
    static final int MAX_ASSIGNMENTS = 10_000;
    private static final int MAX_CELL_CHARS = 32_767;
    private static final int MAX_SHEET_CHARS = 8 * 1024 * 1024;
    private static final int MAX_FILE_BYTES = 8 * 1024 * 1024;
    private static final String CONTENT_TYPES =
            "<?xml version=\"1.0\" encoding=\"UTF-8\"?>"
                    + "<Types xmlns=\"http://schemas.openxmlformats.org/package/2006/content-types\">"
                    + "<Default Extension=\"rels\" ContentType=\"application/vnd.openxmlformats-package.relationships+xml\"/>"
                    + "<Default Extension=\"xml\" ContentType=\"application/xml\"/>"
                    + "<Override PartName=\"/xl/workbook.xml\" ContentType=\"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml\"/>"
                    + "<Override PartName=\"/xl/worksheets/sheet1.xml\" ContentType=\"application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml\"/>"
                    + "</Types>";
    private static final String ROOT_RELS =
            "<?xml version=\"1.0\" encoding=\"UTF-8\"?>"
                    + "<Relationships xmlns=\"http://schemas.openxmlformats.org/package/2006/relationships\">"
                    + "<Relationship Id=\"rId1\" Type=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument\" Target=\"xl/workbook.xml\"/>"
                    + "</Relationships>";
    private static final String WORKBOOK =
            "<?xml version=\"1.0\" encoding=\"UTF-8\"?>"
                    + "<workbook xmlns=\"http://schemas.openxmlformats.org/spreadsheetml/2006/main\""
                    + " xmlns:r=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships\">"
                    + "<bookViews><workbookView/></bookViews>"
                    + "<sheets><sheet name=\"页面汇总\" sheetId=\"1\" r:id=\"rId1\"/></sheets></workbook>";
    private static final String WORKBOOK_RELS =
            "<?xml version=\"1.0\" encoding=\"UTF-8\"?>"
                    + "<Relationships xmlns=\"http://schemas.openxmlformats.org/package/2006/relationships\">"
                    + "<Relationship Id=\"rId1\" Type=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet\" Target=\"worksheets/sheet1.xml\"/>"
                    + "</Relationships>";

    record PageRow(AccessQueryViews.PageInfo page, List<AccessQueryViews.UserIdentity> users) {}

    private AccessQueryWorkbook() {}

    static void requireAssignmentLimit(int count) {
        if (count > MAX_ASSIGNMENTS) throw BusinessException.error(ErrorCode.PAYLOAD_TOO_LARGE);
    }

    static byte[] write(List<PageRow> rows) {
        try {
            ByteArrayOutputStream buffer = new ByteArrayOutputStream();
            try (ZipOutputStream zip =
                    new ZipOutputStream(new LimitedOutputStream(buffer, MAX_FILE_BYTES))) {
                entry(zip, "[Content_Types].xml", CONTENT_TYPES);
                entry(zip, "_rels/.rels", ROOT_RELS);
                entry(zip, "xl/workbook.xml", WORKBOOK);
                entry(zip, "xl/_rels/workbook.xml.rels", WORKBOOK_RELS);
                entry(zip, "xl/worksheets/sheet1.xml", sheet(rows));
            }
            return buffer.toByteArray();
        } catch (IOException | IllegalArgumentException exception) {
            throw BusinessException.error(ErrorCode.PAYLOAD_TOO_LARGE);
        }
    }

    private static void entry(ZipOutputStream zip, String path, String xml) throws IOException {
        zip.putNextEntry(new ZipEntry(path));
        zip.write(xml.getBytes(StandardCharsets.UTF_8));
        zip.closeEntry();
    }

    private static String sheet(List<PageRow> rows) {
        StringBuilder xml = new StringBuilder(1024 + rows.size() * 256);
        xml.append("<?xml version=\"1.0\" encoding=\"UTF-8\"?>");
        xml.append(
                "<worksheet xmlns=\"http://schemas.openxmlformats.org/spreadsheetml/2006/main\">");
        xml.append(
                "<sheetViews><sheetView workbookViewId=\"0\"><pane ySplit=\"1\" topLeftCell=\"A2\" activePane=\"bottomLeft\" state=\"frozen\"/></sheetView></sheetViews>");
        xml.append("<cols><col min=\"1\" max=\"1\" width=\"40\" customWidth=\"1\"/>")
                .append("<col min=\"2\" max=\"2\" width=\"25\" customWidth=\"1\"/>")
                .append("<col min=\"3\" max=\"3\" width=\"18\" customWidth=\"1\"/>")
                .append("<col min=\"4\" max=\"4\" width=\"40\" customWidth=\"1\"/>")
                .append("</cols>");
        xml.append("<sheetData>");
        rowStart(xml, 1);
        textCell(xml, "A1", "页面");
        textCell(xml, "B1", "当前具备资格账号数");
        textCell(xml, "C1", "账号");
        textCell(xml, "D1", "部门");
        xml.append("</row>");
        for (int i = 0; i < rows.size(); i++) {
            PageRow row = rows.get(i);
            int number = i + 2;
            rowStart(xml, number);
            textCell(xml, "A" + number, row.page().menuPath());
            numberCell(xml, "B" + number, row.users().size());
            textCell(
                    xml,
                    "C" + number,
                    row.users().stream()
                            .map(user -> safeText(user.username()))
                            .collect(Collectors.joining("；")));
            textCell(
                    xml,
                    "D" + number,
                    row.users().stream()
                            .map(AccessQueryWorkbook::departmentText)
                            .collect(Collectors.joining("；")));
            xml.append("</row>");
            if (xml.length() > MAX_SHEET_CHARS)
                throw BusinessException.error(ErrorCode.PAYLOAD_TOO_LARGE);
        }
        xml.append("</sheetData><autoFilter ref=\"A1:D").append(rows.size() + 1);
        xml.append("\"/></worksheet>");
        return xml.toString();
    }

    private static String departmentText(AccessQueryViews.UserIdentity user) {
        String names =
                user.departments().isEmpty()
                        ? "未分配"
                        : user.departments().stream()
                                .map(department -> safeText(department.name()))
                                .collect(Collectors.joining("、"));
        return safeText(user.username()) + "（" + names + "）";
    }

    private static void rowStart(StringBuilder xml, int number) {
        xml.append("<row r=\"").append(number).append("\">");
    }

    private static void numberCell(StringBuilder xml, String ref, int value) {
        xml.append("<c r=\"").append(ref).append("\"><v>").append(value).append("</v></c>");
    }

    private static void textCell(StringBuilder xml, String ref, String raw) {
        String value = safeText(raw);
        if (value.length() > MAX_CELL_CHARS)
            throw BusinessException.error(ErrorCode.PAYLOAD_TOO_LARGE);
        xml.append("<c r=\"")
                .append(ref)
                .append("\" t=\"inlineStr\"><is><t xml:space=\"preserve\">");
        for (int offset = 0; offset < value.length(); ) {
            int codePoint = value.codePointAt(offset);
            offset += Character.charCount(codePoint);
            switch (codePoint) {
                case '&' -> xml.append("&amp;");
                case '<' -> xml.append("&lt;");
                case '>' -> xml.append("&gt;");
                default -> xml.appendCodePoint(codePoint);
            }
        }
        xml.append("</t></is></c>");
    }

    /** Prefix suspicious spreadsheet-leading text and remove XML-invalid controls. */
    static String safeText(String source) {
        if (source == null) return "";
        StringBuilder clean = new StringBuilder(source.length());
        source.codePoints()
                .filter(
                        cp ->
                                cp == 9
                                        || cp == 10
                                        || cp == 13
                                        || cp >= 32 && cp <= 0xD7FF
                                        || cp >= 0xE000 && cp <= 0xFFFD
                                        || cp >= 0x10000 && cp <= 0x10FFFF)
                .forEach(clean::appendCodePoint);
        String value = clean.toString();
        String leading = value.stripLeading();
        if (!leading.isEmpty() && "=+-@".indexOf(leading.charAt(0)) >= 0) return "'" + value;
        return value;
    }

    private static final class LimitedOutputStream extends FilterOutputStream {
        private final int limit;
        private int written;

        private LimitedOutputStream(OutputStream delegate, int limit) {
            super(delegate);
            this.limit = limit;
        }

        @Override
        public void write(int value) throws IOException {
            if (++written > limit) throw new IOException("XLSX size limit");
            out.write(value);
        }

        @Override
        public void write(byte[] bytes, int offset, int length) throws IOException {
            if (length > limit - written) throw new IOException("XLSX size limit");
            out.write(bytes, offset, length);
            written += length;
        }
    }
}
