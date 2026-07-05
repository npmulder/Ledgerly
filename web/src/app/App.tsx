import { Route, Routes } from "react-router-dom";

import { DevComponentsScreen } from "@/screens/DevComponentsScreen";
import { DevTokensScreen } from "@/screens/DevTokensScreen";
import { HomeScreen } from "@/screens/HomeScreen";

export function App() {
  return (
    <Routes>
      <Route path="/" element={<HomeScreen />} />
      <Route path="/dev/components" element={<DevComponentsScreen />} />
      <Route path="/dev/tokens" element={<DevTokensScreen />} />
    </Routes>
  );
}
