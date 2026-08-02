local Keywords = {}

function Keywords.lensName(photo)
    local matches = {}
    for _, keyword in ipairs(photo:getRawMetadata("keywords") or {}) do
        local parent = keyword:getParent()
        if parent and parent:getName() == "Curator Lens" then
            table.insert(matches, keyword:getName())
        end
    end
    if #matches > 1 then
        return nil, "Assign only one keyword directly below Curator Lens"
    end
    return matches[1] or "", nil
end

return Keywords