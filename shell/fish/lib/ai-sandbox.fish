function __ai_sandbox_workspace_quota --argument-names launcher_file agent
    set -l versions_file (dirname (dirname (dirname (realpath "$launcher_file"))))/versions.env
    if not test -r "$versions_file"
        echo "$agent: cannot read $versions_file" >&2
        return 2
    end

    set -l workspace_quota (command awk -F= '$1 == "WORKSPACE_QUOTA" { print $2; exit }' "$versions_file")
    if not string match -rq '^[1-9][0-9]*[KMGT]$' -- "$workspace_quota"
        echo "$agent: WORKSPACE_QUOTA must be a positive K, M, G, or T size" >&2
        return 2
    end

    printf '%s\n' "$workspace_quota"
end
