import React from "react";

export interface Source {
  name: string;
  url: string;
}

interface SidebarProps {
  sources: Source[];
  selectedSource: string;
  onSelectSource: (url: string) => void;
}

const Sidebar: React.FC<SidebarProps> = ({ sources, selectedSource, onSelectSource }) => {
  return (
    <aside
      style={{
        borderRight: "1px solid #ddd",
        padding: "20px",
        background: "#f8f8f8",
        minHeight: "100vh",
      }}
    >
      <h2 style={{ marginBottom: "20px" }}>Sources</h2>
      <ul style={{ listStyle: "none", padding: 0 }}>
        {sources.map((source, idx) => (
          <li key={idx} style={{ marginBottom: "10px" }}>
            <button
              onClick={() => onSelectSource(source.url)}
              style={{
                background: selectedSource === source.url ? "#111" : "#fff",
                color: selectedSource === source.url ? "#fff" : "#111",
                border: "1px solid #ccc",
                borderRadius: "5px",
                padding: "8px 12px",
                width: "100%",
                cursor: "pointer",
                textAlign: "left",
              }}
            >
              {source.name}
            </button>
          </li>
        ))}
      </ul>
    </aside>
  );
};

export default Sidebar;