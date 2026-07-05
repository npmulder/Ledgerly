import { Route, Routes } from "react-router-dom";

import { DevApiScreen } from "@/screens/DevApiScreen";
import { HomeScreen } from "@/screens/HomeScreen";
import { LoginScreen } from "@/screens/LoginScreen";

export function App() {
  return (
    <Routes>
      <Route path="/" element={<HomeScreen />} />
      <Route path="/dev/api" element={<DevApiScreen />} />
      <Route path="/login" element={<LoginScreen />} />
    </Routes>
  );
}
