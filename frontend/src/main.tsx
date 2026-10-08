import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, Navigate, RouterProvider } from "react-router-dom";

import "./index.css";
import { AppProvider } from "./components/AppContext";
import { AuthProvider, RequireAuth } from "./components/AuthContext";
import { Layout } from "./components/Layout";
import { ToastProvider } from "./components/toast";
import { LoginPage } from "./pages/auth/LoginPage";
import { CasePage } from "./pages/cases/CasePage";
import { SplitPage } from "./pages/cases/SplitPage";
import { ChatPage } from "./pages/chat/ChatPage";
import { DocumentDetail } from "./pages/documents/DocumentDetail";
import { DocumentsPage } from "./pages/documents/DocumentsPage";
import { SheetPage } from "./pages/sheets/SheetPage";

const router = createBrowserRouter([
  { path: "/login", element: <LoginPage /> },
  {
    // Everything else needs a session; knowledge bases load only after sign-in.
    element: (
      <RequireAuth>
        <AppProvider>
          <Layout />
        </AppProvider>
      </RequireAuth>
    ),
    children: [
      { index: true, element: <Navigate to="/documents" replace /> },
      { path: "documents", element: <DocumentsPage /> },
      { path: "documents/:id", element: <DocumentDetail /> },
      { path: "chat", element: <ChatPage /> },
      { path: "chat/:sessionId", element: <ChatPage /> },
      { path: "cases", element: <CasePage /> },
      { path: "cases/:caseId", element: <CasePage /> },
      { path: "cases/:caseId/sheets/:sheetId", element: <SheetPage /> },
      { path: "cases/:caseId/split", element: <SplitPage /> },
      { path: "*", element: <Navigate to="/documents" replace /> },
    ],
  },
]);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ToastProvider>
      <AuthProvider>
        <RouterProvider router={router} />
      </AuthProvider>
    </ToastProvider>
  </StrictMode>,
);
