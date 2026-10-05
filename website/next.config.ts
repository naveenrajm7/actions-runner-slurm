import withDoctocat from '@primer/doctocat-nextjs/doctocat.config.js'

const basePath = process.env.NEXT_PUBLIC_DOCTOCAT_BASE_PATH ?? "/actions-runner-slurm"
const repositoryURL = "https://github.com/naveenrajm7/actions-runner-slurm"
const repositorySourcePath = "website"

process.env.NEXT_PUBLIC_DOCTOCAT_BASE_PATH = basePath
process.env.NEXT_PUBLIC_SITE_TITLE = "Actions Runner Slurm"
if (repositoryURL) process.env.NEXT_PUBLIC_REPO = repositoryURL
if (repositorySourcePath) process.env.NEXT_PUBLIC_REPO_SRC_PATH = repositorySourcePath

export default withDoctocat({
  transpilePackages: ['@primer/doctocat-nextjs'],
  output: 'export',
  trailingSlash: true,
  images: {
    unoptimized: true,
  },
  basePath,
})
