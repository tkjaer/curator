local Keywords = {}

local function isLensKeyword(keyword)
    local current = keyword
    while current do
        if current:getName() == "Curator Lens" then return true end
        current = current:getParent()
    end
    return false
end

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

function Keywords.tags(photo)
    local tags = {}
    local seen = {}
    for _, keyword in ipairs(photo:getRawMetadata("keywords") or {}) do
        if not isLensKeyword(keyword) then
            local name = tostring(keyword:getName() or "")
            local key = string.lower(name)
            if name ~= "" and not seen[key] then
                seen[key] = true
                table.insert(tags, name)
            end
        end
    end
    table.sort(tags, function(left, right) return string.lower(left) < string.lower(right) end)
    return tags
end

return Keywords