# Knowledge storage — stateless model

## Behavior

Knowledge bases are **stateless**: the engine never persists the raw uploaded
file. On upload it extracts text, chunks it, computes embeddings, and stores the
chunks + vectors in PostgreSQL/pgvector. That is the only persisted form of the
knowledge and the only thing search reads.

The original **file name** is kept as document metadata (the `file_name` column
on `knowledge_documents`) so the admin file list and search citations show a
real name. It is independent of any file path.

Indexing is **automatic on upload**. The upload request returns immediately with
`status: indexing`; the async indexer transitions the document to `ready` (or
`error`). The admin file list polls and reflects the live status.

There is **no re-index** of an already-uploaded document — to re-index, re-upload
the file. The previous re-index endpoints and the admin re-index button are
removed.

## Why

A ReadWriteOnce knowledge volume on the pod's startup critical path turns a
routine Kubernetes node move into a multi-hour outage when the CSI detach wedges.
Holding only chunks+embeddings in PostgreSQL removes the volume entirely, so a
single-replica pod reschedules to any node in seconds. The file name is metadata,
not the file, so it stays in the row regardless.

## Acceptance criteria

| # | Criterion | Test |
|---|-----------|------|
| AC-1 | Upload stores the original file name; it is NOT derived from the (empty) file path | `TestUploadFileToKB_StatelessStoresName` (L1), `TestKB09_FileNameStoredAndDisplayed` (L2) |
| AC-2 | The file list / single-file GET return the real name, never `"."` | `TestKB09_FileNameStoredAndDisplayed` (L2) |
| AC-3 | Legacy rows (pre-`file_name`) fall back to the path basename | `TestKnowledgeDocumentFileName_LegacyFallback` (L1) |
| AC-4 | No raw file is written to disk on upload | `TestUploadFileToKB_StatelessStoresName` (L1) |
| AC-5 | Unicode and over-255-char file names are handled without error (name clamped to the column width) | `TestUploadFileToKB_UnicodeName`, `TestUploadFileToKB_OverlongNameClamped` (L1) |
| AC-6 | Indexing is automatic on upload; status transitions uploading/indexing → ready/error are visible | admin status polling (`KnowledgePage.tsx`) + MCP Playwright UI run |
| AC-7 | Re-index endpoints are removed → 404 | `TestKB10_ReindexEndpointsGone` (L2) |

## Security

| Gate | Check | Test |
|------|-------|------|
| SCC-01 | Unauthenticated knowledge request → 401 | `TestKB08_RequireAuth` (L2) |
| SCC-03 | Invalid input (unsupported file type) → 400, not 500 | `TestKB11_UnsupportedTypeRejected` (L2) |

## Migration

`migrations/014_add_knowledge_document_file_name.yaml` adds `file_name
varchar(255) NOT NULL DEFAULT ''`. Additive and backward compatible: existing
rows take the empty default and fall back to the path basename via `FileName()`,
so no backfill is required. Rollback is a clean `DROP COLUMN`.
