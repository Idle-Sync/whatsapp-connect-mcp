/** @type {import('next').NextConfig} */
const nextConfig = {
  // No server, no database, nothing to run. Plain files.
  output: 'export',
  trailingSlash: true,
  images: { unoptimized: true },
};

export default nextConfig;
