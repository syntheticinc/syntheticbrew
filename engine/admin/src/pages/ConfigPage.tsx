import { useState, useRef } from 'react';
import { api } from '../api/client';

export default function ConfigPage() {
  const [reloading, setReloading] = useState(false);
  const [reloadResult, setReloadResult] = useState<string | null>(null);
  const [exporting, setExporting] = useState(false);
  const [exportedYaml, setExportedYaml] = useState<string | null>(null);
  const [importing, setImporting] = useState(false);
  const [importResult, setImportResult] = useState<string | null>(null);
  const [importYaml, setImportYaml] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [copySuccess, setCopySuccess] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  function clearMessages() {
    setError(null);
    setReloadResult(null);
    setImportResult(null);
  }

  async function handleReload() {
    setReloading(true);
    clearMessages();
    try {
      const res = await api.reloadConfig();
      setReloadResult(`Config reloaded. ${res.agents_count} agents loaded.`);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Reload failed');
    } finally {
      setReloading(false);
    }
  }

  async function handleExport() {
    setExporting(true);
    clearMessages();
    try {
      const yaml = await api.exportConfig();
      setExportedYaml(yaml);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Export failed');
    } finally {
      setExporting(false);
    }
  }

  function handleDownload() {
    if (!exportedYaml) return;
    const blob = new Blob([exportedYaml], { type: 'application/x-yaml' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'syntheticbrew-config.yaml';
    a.click();
    URL.revokeObjectURL(url);
  }

  async function handleCopy() {
    if (!exportedYaml) return;
    try {
      await navigator.clipboard.writeText(exportedYaml);
      setCopySuccess(true);
      setTimeout(() => setCopySuccess(false), 2000);
    } catch {
      setError('Failed to copy to clipboard');
    }
  }

  async function handleImport() {
    const yamlContent = importYaml.trim();
    if (!yamlContent) {
      setError('No YAML content to import. Paste YAML or upload a file.');
      return;
    }

    setImporting(true);
    clearMessages();
    try {
      const res = await api.importConfig(yamlContent);
      setImportResult(`Config imported. ${res.agents_count} agents loaded.`);
      setImportYaml('');
      if (fileInputRef.current) fileInputRef.current.value = '';
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Import failed');
    } finally {
      setImporting(false);
    }
  }

  async function handleFileSelect() {
    const file = fileInputRef.current?.files?.[0];
    if (!file) return;
    try {
      const text = await file.text();
      setImportYaml(text);
    } catch {
      setError('Failed to read file');
    }
  }

  return (
    <div className="max-w-3xl">
      <h1 className="text-2xl font-bold text-brand-light mb-6">Configuration</h1>

      {error && (
        <div className="mb-4 p-3 bg-red-500/10 border border-red-500/30 rounded-btn text-sm text-red-400">
          {error}
        </div>
      )}
      {reloadResult && (
        <div className="mb-4 p-3 bg-status-active/10 border border-status-active/30 rounded-btn text-sm text-status-active">
          {reloadResult}
        </div>
      )}
      {importResult && (
        <div className="mb-4 p-3 bg-status-active/10 border border-status-active/30 rounded-btn text-sm text-status-active">
          {importResult}
        </div>
      )}

      {/* Reload */}
      <section className="mb-8">
        <div className="bg-brand-dark-alt rounded-card border border-brand-shade3/15 p-5">
          <h2 className="text-lg font-semibold text-brand-light mb-2">Hot Reload</h2>
          <p className="text-sm text-brand-shade3 mb-4">
            Reload agent configuration from the database. Changes take effect immediately.
          </p>
          <button
            onClick={handleReload}
            disabled={reloading}
            className="px-4 py-2 bg-brand-accent text-brand-light rounded-btn text-sm font-medium hover:bg-brand-accent-hover disabled:opacity-50 transition-colors"
          >
            {reloading ? 'Reloading...' : 'Reload Config'}
          </button>
        </div>
      </section>

      {/* Export */}
      <section className="mb-8">
        <div className="bg-brand-dark-alt rounded-card border border-brand-shade3/15 p-5">
          <h2 className="text-lg font-semibold text-brand-light mb-2">Export Configuration</h2>
          <p className="text-sm text-brand-shade3 mb-4">
            Fetch current configuration as YAML. Preview, copy, or download.
          </p>
          <div className="flex gap-3 mb-4">
            <button
              onClick={handleExport}
              disabled={exporting}
              className="px-4 py-2 bg-brand-accent text-brand-light rounded-btn text-sm font-medium hover:bg-brand-accent-hover disabled:opacity-50 transition-colors"
            >
              {exporting ? 'Exporting...' : 'Export Configuration'}
            </button>
            {exportedYaml && (
              <>
                <button
                  onClick={handleDownload}
                  className="px-4 py-2 bg-brand-dark border border-brand-shade3/30 text-brand-shade2 rounded-btn text-sm font-medium hover:text-brand-light transition-colors"
                >
                  Download YAML
                </button>
                <button
                  onClick={handleCopy}
                  className="px-4 py-2 bg-brand-dark border border-brand-shade3/30 text-brand-shade2 rounded-btn text-sm font-medium hover:text-brand-light transition-colors"
                >
                  {copySuccess ? 'Copied!' : 'Copy to Clipboard'}
                </button>
              </>
            )}
          </div>
          {exportedYaml && (
            <pre className="p-4 bg-brand-dark rounded-btn border border-brand-shade3/15 text-xs text-brand-shade2 font-mono whitespace-pre-wrap max-h-96 overflow-y-auto">
              {exportedYaml}
            </pre>
          )}
        </div>
      </section>

      {/* Import */}
      <section className="mb-8">
        <div className="bg-brand-dark-alt rounded-card border border-brand-shade3/15 p-5">
          <h2 className="text-lg font-semibold text-brand-light mb-2">Import Configuration</h2>
          <p className="text-sm text-brand-shade3 mb-4">
            Paste YAML content or upload a .yaml/.yml file. It will be parsed and saved to the database, then reloaded.
          </p>
          <div className="mb-4">
            <label className="block text-xs font-medium text-brand-shade3 uppercase tracking-wider mb-2">
              Upload file
            </label>
            <input
              ref={fileInputRef}
              type="file"
              accept=".yaml,.yml"
              onChange={handleFileSelect}
              className="text-sm text-brand-shade3 file:mr-4 file:py-2 file:px-4 file:rounded-btn file:border-0 file:text-sm file:font-medium file:bg-brand-dark file:text-brand-shade2 hover:file:bg-brand-shade3/20"
            />
          </div>
          <div className="mb-4">
            <label className="block text-xs font-medium text-brand-shade3 uppercase tracking-wider mb-2">
              Or paste YAML
            </label>
            <textarea
              value={importYaml}
              onChange={(e) => setImportYaml(e.target.value)}
              rows={10}
              placeholder="agents:&#10;  - name: my-agent&#10;    system_prompt: ..."
              className="w-full p-3 bg-brand-dark-alt border border-brand-shade3/50 rounded-btn text-sm text-brand-light font-mono placeholder:text-brand-shade3 focus:outline-none focus:border-brand-accent resize-y"
            />
          </div>
          <button
            onClick={handleImport}
            disabled={importing || !importYaml.trim()}
            className="px-4 py-2 bg-brand-accent text-brand-light rounded-btn text-sm font-medium hover:bg-brand-accent-hover disabled:opacity-50 transition-colors"
          >
            {importing ? 'Importing...' : 'Import Configuration'}
          </button>
        </div>
      </section>
    </div>
  );
}
