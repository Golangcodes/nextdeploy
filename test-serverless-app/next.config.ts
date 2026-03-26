import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "standalone",
  cacheHandler: process.env.AWS_REGION ? require.resolve('./cache-handler.js') : undefined,
  cacheMaxMemorySize: 0, // Disable in-memory caching in Lambda
  images: {
    remotePatterns: [
      {
        protocol: 'https',
        hostname: 'images.unsplash.com',
      },
      {
        protocol: 'https',
        hostname: 'lh3.googleusercontent.com',
      },
    ],
  },
  async rewrites() {
    return [
      {
        source: '/api/proxy/:path*',
        destination: 'https://jsonplaceholder.typicode.com/:path*',
      },
    ];
  },
};

export default nextConfig;
