package com.streamfusion.platform.system.access.query;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import com.streamfusion.platform.common.exception.BusinessException;
import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.zip.ZipInputStream;
import javax.xml.XMLConstants;
import javax.xml.parsers.DocumentBuilderFactory;
import org.junit.jupiter.api.Test;
import org.w3c.dom.Document;
import org.w3c.dom.Element;

class AccessQueryWorkbookTest {
    private static AccessQueryViews.PageInfo page(String name) {
        return new AccessQueryViews.PageInfo(
                "1002", name, "/system/users", name, "user", true, true, true, true, List.of());
    }

    private static AccessQueryViews.UserIdentity user(
            String username, List<AccessQueryViews.DepartmentIdentity> departments) {
        return new AccessQueryViews.UserIdentity(
                "5010", username, null, 1, false, false, true, departments);
    }

    @Test
    void exportsTextCellsOnlyAndEscapesUntrustedNames() throws Exception {
        byte[] bytes =
                AccessQueryWorkbook.write(
                        List.of(
                                new AccessQueryWorkbook.PageRow(
                                        page("=SUM(1,1)<&"),
                                        List.of(
                                                user(
                                                        "+account",
                                                        List.of(
                                                                new AccessQueryViews
                                                                        .DepartmentIdentity(
                                                                        "2002", "@ops<&")))))));
        String xml = entry(bytes, "xl/worksheets/sheet1.xml");
        assertThat(xml)
                .contains(
                        "t=\"inlineStr\"",
                        "'=SUM(1,1)&lt;&amp;",
                        "'+account",
                        "'@ops&lt;&amp;",
                        "<c r=\"B2\"><v>1</v></c>");
        assertThat(xml).doesNotContain("<f>", "<f ");
        assertThat(entry(bytes, "[Content_Types].xml")).contains("spreadsheetml.worksheet+xml");
    }

    @Test
    void exportsEmptyPageWithoutLosingZeroAndRejectsOversizedCells() throws Exception {
        String empty =
                entry(
                        AccessQueryWorkbook.write(
                                List.of(new AccessQueryWorkbook.PageRow(page("空页面"), List.of()))),
                        "xl/worksheets/sheet1.xml");
        assertThat(empty).contains("<c r=\"B2\"><v>0</v></c>");
        assertThatThrownBy(
                        () ->
                                AccessQueryWorkbook.write(
                                        List.of(
                                                new AccessQueryWorkbook.PageRow(
                                                        page("长".repeat(32768)), List.of()))))
                .isInstanceOf(BusinessException.class);
    }

    @Test
    void workbookPartsParseAndSheetViewReferencesExistingWorkbookView() throws Exception {
        byte[] bytes =
                AccessQueryWorkbook.write(
                        List.of(
                                new AccessQueryWorkbook.PageRow(
                                        page("用户管理"),
                                        List.of(
                                                user("first", List.of()),
                                                user("second", List.of()))),
                                new AccessQueryWorkbook.PageRow(page("空页面"), List.of())));
        Document types = document(entry(bytes, "[Content_Types].xml"));
        Document rootRels = document(entry(bytes, "_rels/.rels"));
        Document workbook = document(entry(bytes, "xl/workbook.xml"));
        Document workbookRels = document(entry(bytes, "xl/_rels/workbook.xml.rels"));
        Document worksheet = document(entry(bytes, "xl/worksheets/sheet1.xml"));

        assertThat(types.getElementsByTagNameNS("*", "Override").getLength()).isEqualTo(2);
        assertThat(rootRels.getElementsByTagNameNS("*", "Relationship").getLength()).isOne();
        assertThat(workbookRels.getElementsByTagNameNS("*", "Relationship").getLength()).isOne();
        assertThat(workbook.getElementsByTagNameNS("*", "workbookView").getLength()).isOne();
        Element sheet = (Element) workbook.getElementsByTagNameNS("*", "sheet").item(0);
        assertThat(
                        sheet.getAttributeNS(
                                "http://schemas.openxmlformats.org/officeDocument/2006/relationships",
                                "id"))
                .isEqualTo("rId1");
        Element sheetView = (Element) worksheet.getElementsByTagNameNS("*", "sheetView").item(0);
        assertThat(sheetView.getAttribute("workbookViewId")).isEqualTo("0");
        assertThat(worksheet.getElementsByTagNameNS("*", "row").getLength()).isEqualTo(3);
        Element count = (Element) worksheet.getElementsByTagNameNS("*", "c").item(5);
        assertThat(count.getAttribute("r")).isEqualTo("B2");
        assertThat(count.getElementsByTagNameNS("*", "v").item(0).getTextContent()).isEqualTo("2");
        Element emptyCount = (Element) worksheet.getElementsByTagNameNS("*", "c").item(9);
        assertThat(emptyCount.getAttribute("r")).isEqualTo("B3");
        assertThat(emptyCount.getElementsByTagNameNS("*", "v").item(0).getTextContent())
                .isEqualTo("0");
    }

    @Test
    void assignmentLimitAllowsTenThousandAndRejectsNext() {
        AccessQueryWorkbook.requireAssignmentLimit(10_000);
        assertThatThrownBy(() -> AccessQueryWorkbook.requireAssignmentLimit(10_001))
                .isInstanceOf(BusinessException.class);
    }

    @Test
    void rejectsUncompressedSheetBeyondBudgetEvenWhenTextCompressesWell() {
        var repeated =
                user(
                        "account",
                        List.of(
                                new AccessQueryViews.DepartmentIdentity(
                                        "2002", "部门".repeat(4_200))));
        var rows =
                java.util.Collections.nCopies(
                        1_000, new AccessQueryWorkbook.PageRow(page("页面"), List.of(repeated)));
        assertThatThrownBy(() -> AccessQueryWorkbook.write(rows))
                .isInstanceOf(BusinessException.class);
    }

    private static Document document(String xml) throws Exception {
        DocumentBuilderFactory factory = DocumentBuilderFactory.newInstance();
        factory.setNamespaceAware(true);
        factory.setFeature("http://apache.org/xml/features/disallow-doctype-decl", true);
        factory.setFeature("http://xml.org/sax/features/external-general-entities", false);
        factory.setFeature("http://xml.org/sax/features/external-parameter-entities", false);
        factory.setFeature("http://apache.org/xml/features/nonvalidating/load-external-dtd", false);
        factory.setFeature(XMLConstants.FEATURE_SECURE_PROCESSING, true);
        factory.setAttribute(XMLConstants.ACCESS_EXTERNAL_DTD, "");
        factory.setAttribute(XMLConstants.ACCESS_EXTERNAL_SCHEMA, "");
        return factory.newDocumentBuilder()
                .parse(new ByteArrayInputStream(xml.getBytes(StandardCharsets.UTF_8)));
    }

    private static String entry(byte[] archive, String name) throws Exception {
        try (ZipInputStream zip = new ZipInputStream(new ByteArrayInputStream(archive))) {
            java.util.zip.ZipEntry current;
            while ((current = zip.getNextEntry()) != null) {
                if (name.equals(current.getName()))
                    return new String(zip.readAllBytes(), StandardCharsets.UTF_8);
            }
        }
        throw new AssertionError("Missing XLSX part " + name);
    }
}
