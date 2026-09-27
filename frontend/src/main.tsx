import { lazy, StrictMode, Suspense } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, Navigate, RouterProvider } from "react-router-dom";

import "./index.css";
import { AppProvider } from "./components/AppContext";
import { Layout } from "./components/Layout";
import { ToastProvider } from "./components/toast";
import { Loading } from "./components/ui";
import { ChatPage } from "./pages/chat/ChatPage";
import { DocumentDetail } from "./pages/documents/DocumentDetail";
import { DocumentsPage } from "./pages/documents/DocumentsPage";
import { WikiPage } from "./pages/wiki/WikiPage";

// three.js is only loaded when the graph is opened.
const GraphPage = lazy(() => import("./pages/graph/GraphPage").then((m) => ({ default: m.GraphPage })));

const router = createBrowserRouter([
  {
    element: <Layout />,
    children: [
      { index: true, element: <Navigate to="/documents" replace /> },
      { path: "documents", element: <DocumentsPage /> },
      { path: "documents/:id", element: <DocumentDetail /> },
      { path: "chat", element: <ChatPage /> },
      { path: "chat/:sessionId", element: <ChatPage /> },
      { path: "wiki", element: <WikiPage /> },
      { path: "wiki/:caseId/*", element: <WikiPage /> },
      {
        path: "graph",
        element: (
          <Suspense fallback={<Loading />}>
            <GraphPage />
          </Suspense>
        ),
      },
      { path: "*", element: <Navigate to="/documents" replace /> },
    ],
  },
]);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ToastProvider>
      <AppProvider>
        <RouterProvider router={router} />
      </AppProvider>
    </ToastProvider>
  </StrictMode>,
);
