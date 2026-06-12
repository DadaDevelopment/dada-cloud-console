import type { NextConfig } from "next";
import path from "path";

const nextConfig: NextConfig = {
  output: "standalone",
  transpilePackages: ["@dada/react-sso"],
  turbopack: {
    root: path.resolve(__dirname),
  },
};

export default nextConfig;
