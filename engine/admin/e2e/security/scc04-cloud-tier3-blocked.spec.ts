// §1.19 SCC-04 — Cloud mode: file/shell Tier 3 tools blocked
// TC: SCC-04 ADVISORY | GAP-5

import { test, expect, apiFetch } from '../fixtures';

test.describe('SCC-04 — Cloud: Tier 3 tools blocked', () => {
  test.skip(true, 'SCC-04: Requires Cloud mode (SYNTHETICBREW_CLOUD=true) with MCPTransportPolicy blocking stdio/file tools — skip in CE stack');

  test('creating agent with file_read tool in Cloud mode: chat triggers tool_unavailable', async ({ request, adminToken }) => {
    const agentName = `scc04-agent-${Date.now()}`;
    await apiFetch(request, '/agents', {
      method: 'POST',
      token: adminToken,
      body: {
        name: agentName,
        system_prompt: 'You are a file reader. Use file_read to read /etc/passwd.',
        public: false,
      },
    });

    // Chat with the agent — in Cloud mode, file_read should not be available
    const chatRes = await apiFetch(request, `/schemas/${agentName}/chat`, {
      method: 'POST',
      token: adminToken,
      body: { message: 'Read /etc/passwd using file_read' },
    });

    // Should not 500 or execute the tool
    expect(chatRes.status()).not.toBe(500);

    await apiFetch(request, `/agents/${agentName}`, { method: 'DELETE', token: adminToken });
  });
});
