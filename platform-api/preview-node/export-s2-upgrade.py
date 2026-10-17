#!/usr/bin/env python3
"""Generate a guarded S2 -> S3 upgrade; never connect to or modify a database."""
from pathlib import Path
import sys


def render(root: Path) -> str:
    table = root / 'sql' / '业务'
    node = (table / '预览节点表.sql').read_text(encoding='utf-8')
    columns = node[node.index('    priority INT'):node.index('    version BIGINT')]
    additions = ',\n'.join('ADD COLUMN ' + line.strip().rstrip(',') for line in columns.splitlines())
    sources = []
    for name in ('预览源归属表.sql', '预览输出预占表.sql'):
        text = (table / name).read_text(encoding='utf-8')
        sources.append(text[text.index('CREATE TABLE'):])
    return f"""-- Generated from maintained per-table sources. Back up and stop Platform first.
-- S2 only. Stop/confirm all viewers on the node before upgrading. Never delete ownership to pass.
SET NAMES utf8mb4;
DELIMITER //
CREATE PROCEDURE sf_preview_s3_upgrade()
BEGIN
  IF EXISTS(SELECT 1 FROM preview_viewer) OR EXISTS(SELECT 1 FROM preview_operation WHERE state IN ('PENDING','UNKNOWN')) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='Preview ownership must be confirmed empty before S3 upgrade';
  END IF;
  ALTER TABLE preview_node DROP CHECK ck_preview_node_single,
    MODIFY control_uri VARCHAR(512) NULL, MODIFY certificate_sha256 CHAR(64) NULL,
    MODIFY token_path VARCHAR(1024) NULL, MODIFY media_address VARCHAR(253) NULL,
    {additions};
  ALTER TABLE preview_viewer ADD COLUMN output_key VARCHAR(80) NULL,
    ADD COLUMN egress_kbps BIGINT NOT NULL DEFAULT 0, ADD COLUMN is_starting INT NOT NULL DEFAULT 1;
  INSERT INTO preview_node(id,node_id,name,mode,config_version,priority)
    SELECT 0,'local','本机',IF(EXISTS(SELECT 1 FROM preview_node WHERE mode IN ('REMOTE','DRAINING')),'LOCAL','REMOTE'),'local',0;
  {''.join(sources)}
END//
DELIMITER ;
CALL sf_preview_s3_upgrade();
DROP PROCEDURE sf_preview_s3_upgrade;
-- MySQL DDL is not transactional. Stop on any error; inspect before retrying.
"""


if __name__ == '__main__':
    if len(sys.argv) != 2:
        raise SystemExit('Usage: export-s2-upgrade.py new-output.sql')
    output = Path(sys.argv[1])
    with output.open('x', encoding='utf-8', newline='\n') as target:
        target.write(render(Path(__file__).resolve().parent.parent))
