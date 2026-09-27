import { Resource } from "ra-core";
import { Admin } from "@/components/admin";
import { authProvider } from "./authProvider";
import { dataProvider } from "./dataProvider";
import { i18nProvider } from "./i18nProvider";
import { LoginPage } from "./login/LoginPage";
import { Layout } from "./layouts/Layout";
import { Dashboard } from "./dashboard/Dashboard";
import { tenants } from "./resources/tenants";
import { datasets } from "./resources/datasets";
import { users } from "./resources/users";
import { modelProviders } from "./resources/model-providers";
import { apiKeys } from "./resources/api-keys";
import { tasks } from "./resources/tasks";
import { usage } from "./resources/usage";
import { teams } from "./resources/teams";
import { projects } from "./resources/projects";
import { audit } from "./resources/audit";
import { alerts, alertDeliveries } from "./resources/alerts";
import { approvals, approvalPolicies } from "./resources/approvals";
import { roles } from "./resources/roles";
import { branding } from "./resources/branding";
import { chats } from "./resources/chats";
import { searchApps } from "./resources/search-apps";
import { scenarioTemplates } from "./resources/scenario-templates";
import { knowledgeOps } from "./resources/knowledge-ops";
import { assetGovernance } from "./resources/asset-governance";
import { releaseGovernance } from "./resources/release-governance";
import { assistantReleases } from "./resources/assistant-releases";
import { memories } from "./resources/memories";
import { agents } from "./resources/agents";
import { enterpriseConnections } from "./resources/enterprise-connections";
import { workbench } from "./resources/workbench";
import { conversationCenter } from "./resources/conversation-center";
import { system } from "./resources/system";

export default function App() {
  return (
    <Admin
      dataProvider={dataProvider}
      authProvider={authProvider}
      i18nProvider={i18nProvider}
      loginPage={LoginPage}
      layout={Layout}
      dashboard={Dashboard}
      requireAuth
      title="RAGFlow-X"
    >
      {(permissions) => {
        const { approvalsEnabled = false } = (permissions ?? {}) as { approvalsEnabled?: boolean };
        return (
          <>
            <Resource {...tenants} />
            <Resource {...datasets} />
            <Resource {...users} />
            <Resource {...modelProviders} />
            <Resource {...apiKeys} />
            <Resource {...tasks} />
            <Resource {...usage} />
            <Resource {...teams} />
            <Resource {...projects} />
            <Resource {...audit} />
            <Resource {...alerts} />
            <Resource {...alertDeliveries} />
            {approvalsEnabled && <Resource {...approvals} />}
            {approvalsEnabled && <Resource {...approvalPolicies} />}
            <Resource {...roles} />
            <Resource {...branding} />
            <Resource {...chats} />
            <Resource {...searchApps} />
            <Resource {...scenarioTemplates} />
            <Resource {...knowledgeOps} />
            <Resource {...assetGovernance} />
            <Resource {...releaseGovernance} />
            <Resource {...assistantReleases} />
            <Resource {...memories} />
            <Resource {...agents} />
            <Resource {...enterpriseConnections} />
            <Resource {...system} />
            <Resource {...workbench} />
            <Resource {...conversationCenter} />
          </>
        );
      }}
    </Admin>
  );
}

