import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { AuthContext, useAuthProvider } from './hooks/useAuth';
import Layout from './components/Layout';
import OnboardingGate from './components/OnboardingGate';
import MCPPage from './pages/MCPPage';
import ModelsPage from './pages/ModelsPage';
import TasksPage from './pages/TasksPage';
import SettingsPage from './pages/SettingsPage';
import APIKeysPage from './pages/APIKeysPage';
import ConfigPage from './pages/ConfigPage';
import AuditPage from './pages/AuditPage';
import ResiliencePage from './pages/ResiliencePage';
import ToolCallLogPage from './pages/ToolCallLogPage';
import AgentDrillInPage from './pages/AgentDrillInPage';
import AgentsPage from './pages/AgentsPage';
import WidgetConfigPage from './pages/WidgetConfigPage';
import KnowledgePage from './pages/KnowledgePage';
import KnowledgeGraphsPage from './pages/KnowledgeGraphsPage';
import KnowledgeGraphDetailPage from './pages/KnowledgeGraphDetailPage';
import KnowledgeGraphEntitiesPage from './pages/KnowledgeGraphEntitiesPage';
import OverviewPage from './pages/OverviewPage';
import SchemasPage from './pages/SchemasPage';
import SchemaDetailPage from './pages/SchemaDetailPage';
import OnboardingWizard from './pages/OnboardingWizard';
import OAuthConsentPage from './pages/OAuthConsentPage';
// Wave 1+7 auth: the SPA never renders a login page. `useAuthProvider`
// bootstraps a token via `auth/local-session` (local mode) or a URL hash
// fragment (external mode) on mount. Until that completes we render a
// neutral splash so we don't flash an empty app.
function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const token = localStorage.getItem('jwt');
  if (!token) {
    return (
      <div className="fixed inset-0 bg-brand-dark flex items-center justify-center">
        <div className="text-sm text-brand-shade3 font-mono">Authenticating…</div>
      </div>
    );
  }
  return <>{children}</>;
}

export default function App() {
  const auth = useAuthProvider();

  return (
    <AuthContext.Provider value={auth}>
      <BrowserRouter basename={import.meta.env.BASE_URL}>
        <Routes>
          <Route
            path="/onboarding"
            element={
              <ProtectedRoute>
                <OnboardingGate>
                  <OnboardingWizard />
                </OnboardingGate>
              </ProtectedRoute>
            }
          />
          {/* OAuth consent needs an authenticated admin session (so the engine
              can bind the consent nonce to the subject) but bypasses the
              onboarding gate — a coding agent connecting must not be forced
              through the wizard. Works in both local and external auth modes. */}
          <Route
            path="/oauth/consent"
            element={
              <ProtectedRoute>
                <OAuthConsentPage />
              </ProtectedRoute>
            }
          />
          <Route
            element={
              <ProtectedRoute>
                <OnboardingGate>
                  <Layout />
                </OnboardingGate>
              </ProtectedRoute>
            }
          >
            <Route path="/" element={<OverviewPage />} />
            <Route path="/overview" element={<OverviewPage />} />
            <Route path="/schemas" element={<SchemasPage />} />
            <Route path="/schemas/:schemaName" element={<SchemaDetailPage />} />
            <Route path="/schemas/:schemaName/:agentName" element={<AgentDrillInPage />} />
            <Route path="/mcp" element={<MCPPage />} />
            <Route path="/models" element={<ModelsPage />} />
            <Route path="/tasks" element={<TasksPage />} />
            <Route path="/settings" element={<SettingsPage />} />
            <Route path="/api-keys" element={<APIKeysPage />} />
            <Route path="/config" element={<ConfigPage />} />
            <Route path="/audit" element={<AuditPage />} />
            <Route path="/resilience" element={<ResiliencePage />} />
            <Route path="/tool-call-log" element={<ToolCallLogPage />} />
            <Route path="/knowledge" element={<KnowledgePage />} />
            <Route path="/knowledge-graphs" element={<KnowledgeGraphsPage />} />
            <Route path="/knowledge-graphs/:bundle" element={<KnowledgeGraphDetailPage />} />
            <Route path="/knowledge-graphs/:bundle/entities/:entityType" element={<KnowledgeGraphEntitiesPage />} />
            <Route path="/widget" element={<WidgetConfigPage />} />
            <Route path="/agents" element={<AgentsPage />} />
            <Route path="/agents/:agent" element={<AgentDrillInPage />} />
          </Route>
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </BrowserRouter>
    </AuthContext.Provider>
  );
}
