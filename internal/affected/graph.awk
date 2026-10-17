# graph.awk - the dependency-graph half of affected detection
# (enginekit/pipelineaffected, ankra-efrnzf.19).
#
# POSIX awk only: it runs under busybox awk in the checkout image, mawk and
# gawk on Linux, and the BSD awk a laptop has. No gensub, no length() of an
# array, no delete of a whole array, no interval expressions in a regular
# expression.
#
# One program, three modes (-v mode=...):
#
#   modules   reads the tracked file list and prints every Go module the
#             repository holds, one "M<TAB>root<TAB>module path" line each,
#             and every local replace directive as "R<TAB>dir<TAB>module path".
#   regex     reads that module list and prints one extended regular
#             expression matching any of the module paths, for git grep.
#   analyze   reads files, modules, edges, changed and targets (in that order,
#             named on the command line) and appends one
#             "ankra.affected.<key>=true|false" line per target key to the
#             file named by -v out. A key whose targets resolve to nothing
#             gets no line at all, which the platform reads as "run it".
#
# Every rule below errs toward "affected": a string literal that merely looks
# like an import is an edge, every build tag is on, every file in a package
# directory (testdata, embedded files) belongs to the package, and a change
# to go.mod, go.sum, go.work or a vendor tree reaches every package of its
# module. A missed edge would skip a stage that should have run; an extra one
# only runs a stage that could have been skipped.

function base_name(path,   slash) {
	slash = last_slash(path)
	return slash ? substr(path, slash + 1) : path
}

function dir_name(path,   slash) {
	slash = last_slash(path)
	return slash ? substr(path, 1, slash - 1) : "."
}

function last_slash(path,   position) {
	for (position = length(path); position > 0; position--) {
		if (substr(path, position, 1) == "/") {
			return position
		}
	}
	return 0
}

# is_within reports whether path is directory itself or lies below it. The
# repository root "." contains everything.
function is_within(directory, path) {
	if (directory == "." || path == directory) {
		return 1
	}
	return substr(path, 1, length(directory) + 1) == directory "/"
}

function join_path(directory, relative) {
	if (directory == "." || directory == "") {
		return relative
	}
	if (relative == "" || relative == ".") {
		return directory
	}
	return directory "/" relative
}

# clean_path resolves "." and ".." segments. A path that climbs above the
# repository root answers "" - it names nothing this tree holds.
function clean_path(path,   segments, count, index_, kept, depth, result) {
	count = split(path, segments, "/")
	depth = 0
	for (index_ = 1; index_ <= count; index_++) {
		if (segments[index_] == "" || segments[index_] == ".") {
			continue
		}
		if (segments[index_] == "..") {
			if (depth == 0) {
				return ""
			}
			depth--
			continue
		}
		depth++
		kept[depth] = segments[index_]
	}
	if (depth == 0) {
		return "."
	}
	result = kept[1]
	for (index_ = 2; index_ <= depth; index_++) {
		result = result "/" kept[index_]
	}
	return result
}

# is_go_ignored reports a directory the go command never treats as a
# package: one inside testdata or vendor, or below a directory whose name
# starts with "." or "_". Its files still belong to the package around it.
function is_go_ignored(directory,   segments, count, index_, first) {
	if (directory == ".") {
		return 0
	}
	count = split(directory, segments, "/")
	for (index_ = 1; index_ <= count; index_++) {
		first = substr(segments[index_], 1, 1)
		if (segments[index_] == "testdata" || segments[index_] == "vendor" || first == "." || first == "_") {
			return 1
		}
	}
	return 0
}

function has_segment(path, name,   segments, count, index_) {
	count = split(path, segments, "/")
	for (index_ = 1; index_ <= count; index_++) {
		if (segments[index_] == name) {
			return 1
		}
	}
	return 0
}

function trim(text) {
	sub(/^[ \t\r]+/, "", text)
	sub(/[ \t\r]+$/, "", text)
	return text
}

function unquote_go(text) {
	text = trim(text)
	if (text ~ /^".*"$/ || text ~ /^`.*`$/) {
		return substr(text, 2, length(text) - 2)
	}
	return text
}

# read_go_mod prints the module directive and the local replace directives
# of one go.mod. A replace whose right-hand side is a directory in this
# repository maps the replaced module path onto that directory, so an import
# of the replaced path resolves to the files the build actually compiles.
function read_go_mod(file,   directory, line, in_replace_block, fields, count, arrow, left, right, target) {
	directory = dir_name(file)
	in_replace_block = 0
	while ((getline line < file) > 0) {
		sub(/\/\/.*/, "", line)
		line = trim(line)
		if (line == "") {
			continue
		}
		if (in_replace_block) {
			if (line == ")") {
				in_replace_block = 0
				continue
			}
			print_replace(directory, line)
			continue
		}
		if (line ~ /^module[ \t]/) {
			count = split(line, fields, /[ \t]+/)
			if (count >= 2) {
				printf "M\t%s\t%s\n", directory, unquote_go(fields[2])
			}
			continue
		}
		if (line ~ /^replace[ \t]*\($/) {
			in_replace_block = 1
			continue
		}
		if (line ~ /^replace[ \t]/) {
			sub(/^replace[ \t]+/, "", line)
			print_replace(directory, line)
		}
	}
	close(file)
}

function print_replace(directory, line,   arrow, left, right, fields, count, target) {
	arrow = index(line, "=>")
	if (!arrow) {
		return
	}
	left = trim(substr(line, 1, arrow - 1))
	right = trim(substr(line, arrow + 2))
	count = split(left, fields, /[ \t]+/)
	left = unquote_go(fields[1])
	count = split(right, fields, /[ \t]+/)
	if (count != 1) {
		return
	}
	right = unquote_go(fields[1])
	if (right !~ /^\.\.?\//) {
		return
	}
	target = clean_path(join_path(directory, right))
	if (target != "") {
		printf "R\t%s\t%s\n", target, left
	}
}

function escape_regex(text,   result, position, character) {
	result = ""
	for (position = 1; position <= length(text); position++) {
		character = substr(text, position, 1)
		if (index(".[]()*+?{}|^$\\", character)) {
			result = result "\\" character
		} else {
			result = result character
		}
	}
	return result
}

# ---------------------------------------------------------------------------
# Go: module table, packages, edges.

# module_of answers the root of the Go module directory belongs to, the
# longest module root containing it, or "" outside every module.
function module_of(directory,   index_, best, best_length, root_length) {
	best = ""
	best_length = -1
	for (index_ = 1; index_ <= module_count; index_++) {
		if (!is_within(module_root[index_], directory)) {
			continue
		}
		root_length = module_root[index_] == "." ? 0 : length(module_root[index_])
		if (root_length > best_length) {
			best = module_root[index_]
			best_length = root_length
		}
	}
	return best
}

# resolve_import answers the directory an import path names in this
# repository, through the longest module path (or local replace) it falls
# under, or "" when it names nothing here.
function resolve_import(import_path,   index_, best, best_length, rest) {
	best = 0
	best_length = -1
	for (index_ = 1; index_ <= mapping_count; index_++) {
		if (import_path == mapping_path[index_] ||
			substr(import_path, 1, length(mapping_path[index_]) + 1) == mapping_path[index_] "/") {
			if (length(mapping_path[index_]) > best_length) {
				best = index_
				best_length = length(mapping_path[index_])
			}
		}
	}
	if (!best) {
		return ""
	}
	rest = substr(import_path, best_length + 2)
	return clean_path(join_path(mapping_root[best], rest))
}

# add_go_edge records that a package's code imports another. An import made
# only by a _test.go file is kept apart: `go test` compiles a package's own
# tests, never the tests of what it imports, so a test-only edge counts at a
# stage's own packages and nowhere below them - the closure
# `go list -deps -test` gives.
function add_go_edge(from, to, is_test) {
	if (from == to) {
		return
	}
	if (is_test) {
		if ((from, to) in go_test_edge_seen) {
			return
		}
		go_test_edge_seen[from, to] = 1
		go_test_edges[from] = (from in go_test_edges) ? go_test_edges[from] SUBSEP to : to
		return
	}
	if ((from, to) in go_edge_seen) {
		return
	}
	go_edge_seen[from, to] = 1
	go_edges[from] = (from in go_edges) ? go_edges[from] SUBSEP to : to
}

function add_project_edge(from, to) {
	if (from == to || ((from, to) in project_edge_seen)) {
		return
	}
	project_edge_seen[from, to] = 1
	project_edges[from] = (from in project_edges) ? project_edges[from] SUBSEP to : to
}

# ---------------------------------------------------------------------------
# pnpm: projects and their package.json references.

# read_package_json records one project: its directory, its top-level "name",
# and every string the manifest carries, which become edges once every
# project's name is known. Any string naming another project is an edge -
# a dependency key, a workspace: or npm: alias - and a file: or link: path
# into another project is one too.
function read_package_json(file,   directory, content, line, depth, token, rest, is_key, expect_name, name, strings_seen) {
	directory = dir_name(file)
	project_count++
	project_dir[project_count] = directory
	project_index_by_dir[directory] = project_count
	content = ""
	while ((getline line < file) > 0) {
		content = content line "\n"
	}
	close(file)
	depth = 0
	expect_name = 0
	name = ""
	strings_seen = ""
	while (match(content, /"([^"\\]|\\.)*"|[][{}]/)) {
		token = substr(content, RSTART, RLENGTH)
		content = substr(content, RSTART + RLENGTH)
		if (token == "{" || token == "[") {
			depth++
			expect_name = 0
			continue
		}
		if (token == "}" || token == "]") {
			depth--
			expect_name = 0
			continue
		}
		token = substr(token, 2, length(token) - 2)
		rest = content
		sub(/^[ \t\r\n]*/, "", rest)
		is_key = substr(rest, 1, 1) == ":"
		if (expect_name && !is_key) {
			if (name == "") {
				name = token
			}
			expect_name = 0
			continue
		}
		expect_name = depth == 1 && is_key && token == "name"
		strings_seen = strings_seen SUBSEP token
	}
	project_name[project_count] = name
	project_strings[project_count] = strings_seen
	if (name != "") {
		if (name in project_by_name) {
			project_by_name[name] = project_by_name[name] SUBSEP project_count
		} else {
			project_by_name[name] = project_count
		}
	}
}

# reference_name strips what a dependency specifier wraps a package name in:
# workspace: and npm: prefixes and a trailing @version.
function reference_name(text,   at) {
	if (substr(text, 1, 10) == "workspace:") {
		text = substr(text, 11)
	} else if (substr(text, 1, 4) == "npm:") {
		text = substr(text, 5)
	}
	if (substr(text, 1, 1) == "@") {
		at = index(substr(text, 2), "@")
		if (at) {
			text = substr(text, 1, at)
		}
	} else {
		at = index(text, "@")
		if (at > 1) {
			text = substr(text, 1, at - 1)
		}
	}
	return text
}

function link_project_edges(project,   texts, count, index_, text, target, names, name_count, name_index, path) {
	count = split(project_strings[project], texts, SUBSEP)
	for (index_ = 1; index_ <= count; index_++) {
		text = texts[index_]
		if (text == "") {
			continue
		}
		if (substr(text, 1, 5) == "file:" || substr(text, 1, 5) == "link:") {
			path = clean_path(join_path(project_dir[project], substr(text, 6)))
			if (path != "" && (path in project_index_by_dir)) {
				add_project_edge(project, project_index_by_dir[path])
			}
			continue
		}
		link_named(project, text)
		link_named(project, reference_name(text))
	}
}

function link_named(project, name,   names, count, index_) {
	if (!(name in project_by_name)) {
		return
	}
	count = split(project_by_name[name], names, SUBSEP)
	for (index_ = 1; index_ <= count; index_++) {
		add_project_edge(project, names[index_] + 0)
	}
}

# pnpm_owner answers the project a changed file belongs to: the deepest
# project directory above it other than the repository root, or 0.
function pnpm_owner(path,   index_, best, best_length) {
	best = 0
	best_length = -1
	for (index_ = 1; index_ <= project_count; index_++) {
		if (project_dir[index_] == ".") {
			continue
		}
		if (is_within(project_dir[index_], path) && length(project_dir[index_]) > best_length) {
			best = index_
			best_length = length(project_dir[index_])
		}
	}
	return best
}

# is_outside_pnpm reports a repository-root file that cannot change what a
# workspace package builds or tests: documentation, and the Go module files a
# polyglot repository keeps at its root.
function is_outside_pnpm(path,   lowered) {
	lowered = tolower(path)
	return lowered ~ /\.md$/ || lowered ~ /^license/ || lowered ~ /^licence/ || lowered ~ /^changelog/ ||
		lowered == ".gitignore" || lowered == ".gitattributes" || lowered ~ /\.go$/ ||
		lowered == "go.mod" || lowered == "go.sum" || lowered == "go.work" || lowered == "go.work.sum"
}

# ---------------------------------------------------------------------------

BEGIN {
	FS = "\n"
	if (mode == "analyze") {
		files_name = ARGV[1]
		modules_name = ARGV[2]
		edges_name = ARGV[3]
		changed_name = ARGV[4]
		targets_name = ARGV[5]
	}
}

mode == "modules" {
	if (substr($0, 1, 1) == "\"") {
		unreadable_names = 1
		next
	}
	if (base_name($0) == "go.mod" && !is_go_ignored(dir_name($0))) {
		read_go_mod($0)
	}
	next
}

mode == "regex" {
	split($0, fields, "\t")
	if (fields[3] == "" || (fields[3] in regex_seen)) {
		next
	}
	regex_seen[fields[3]] = 1
	regex = regex (regex == "" ? "" : "|") escape_regex(fields[3])
	next
}

mode == "analyze" && FILENAME == files_name {
	path = $0
	if (path == "") {
		next
	}
	if (substr(path, 1, 1) == "\"") {
		unreadable_names = 1
		next
	}
	directory = dir_name(path)
	if (path ~ /\.go$/ && !is_go_ignored(directory)) {
		if (index(path, ":")) {
			unreadable_names = 1
		}
		is_package[directory] = 1
	}
	if (base_name(path) == "package.json" && !has_segment(path, "node_modules")) {
		package_json_count++
		package_json[package_json_count] = path
	}
	next
}

mode == "analyze" && FILENAME == modules_name {
	split($0, fields, "\t")
	if (fields[1] == "M") {
		is_module_root[fields[2]] = 1
		module_count++
		module_root[module_count] = fields[2]
		module_path[module_count] = fields[3]
	}
	mapping_count++
	mapping_root[mapping_count] = fields[2]
	mapping_path[mapping_count] = fields[3]
	next
}

mode == "analyze" && FILENAME == edges_name {
	# git grep -o prints "<file>:<the quoted import path>".
	colon = index($0, ":")
	if (!colon) {
		next
	}
	file = substr($0, 1, colon - 1)
	directory = dir_name(file)
	if (!(directory in is_package)) {
		next
	}
	target = resolve_import(substr($0, colon + 2, length($0) - colon - 2))
	if (target != "" && (target in is_package)) {
		add_go_edge(directory, target, file ~ /_test\.go$/)
	}
	next
}

mode == "analyze" && FILENAME == changed_name {
	if ($0 != "") {
		changed[++changed_count] = $0
	}
	next
}

mode == "analyze" && FILENAME == targets_name {
	split($0, fields, "\t")
	if (fields[1] == "") {
		next
	}
	if (!(fields[1] in key_seen)) {
		key_seen[fields[1]] = 1
		key_order[++key_count] = fields[1]
	}
	target_count++
	target_key[target_count] = fields[1]
	target_ecosystem[target_count] = fields[2]
	target_pattern[target_count] = fields[3]
	next
}

END {
	if (mode == "modules") {
		exit unreadable_names ? 3 : 0
	}
	if (mode == "regex") {
		print regex
		exit 0
	}
	if (mode != "analyze") {
		exit 2
	}
	if (unreadable_names) {
		print "[ankra] affected: the repository has a file name the analysis cannot read safely, so every stage that depends on the graph runs" > "/dev/stderr"
		exit 3
	}
	analyze()
}

function analyze(   index_, package, project, path, base, directory, module, owner) {
	for (package in is_package) {
		package_module[package] = module_of(package)
	}

	for (index_ = 1; index_ <= package_json_count; index_++) {
		read_package_json(package_json[index_])
	}
	for (project = 1; project <= project_count; project++) {
		link_project_edges(project)
	}

	# What the change touched.
	for (index_ = 1; index_ <= changed_count; index_++) {
		path = changed[index_]
		base = base_name(path)
		directory = dir_name(path)
		if (base == "go.work" || base == "go.work.sum") {
			# The go command finds go.work in the directory it runs in or
			# above, so one reaches every package at or below its own
			# directory; at the root, every Go stage.
			if (directory == ".") {
				go_everything = path
			} else {
				for (package in is_package) {
					if (is_within(directory, package) && !(package in changed_package)) {
						changed_package[package] = path
					}
				}
			}
		} else if (base == "go.mod" || base == "go.sum" || has_segment(directory, "vendor")) {
			module = module_of(directory)
			if (module != "" && !(module in changed_module)) {
				changed_module[module] = path
			}
			if ((base == "go.mod" || base == "go.sum") && !(directory in is_module_root)) {
				# A go.mod that is no longer a module root - a module
				# removed or moved - reaches everything that was in it.
				for (package in is_package) {
					if (is_within(directory, package) && !(package in changed_package)) {
						changed_package[package] = path
					}
				}
			}
		}
		owner = go_owner(directory)
		if (owner != "" && !(owner in changed_package)) {
			changed_package[owner] = path
		}

		owner = pnpm_owner(path)
		if (owner) {
			if (!(owner in changed_project)) {
				changed_project[owner] = path
			}
		} else if ((index(path, "/") == 0 && !is_outside_pnpm(path)) || substr(path, 1, 8) == "patches/") {
			if (pnpm_everything == "") {
				pnpm_everything = path
			}
		}
		# A pnpm patch (patchedDependencies) may live in any directory the
		# root manifest names, and changes what every project installs.
		if (path ~ /\.patch$/ && project_count > 0) {
			if (pnpm_everything == "") {
				pnpm_everything = path
			}
		}
	}

	for (index_ = 1; index_ <= key_count; index_++) {
		decide(key_order[index_])
	}
}

# go_owner answers the package a changed file belongs to: the nearest
# package directory at or above it inside the same module, or "".
function go_owner(directory,   module) {
	module = module_of(directory)
	if (module == "") {
		return ""
	}
	while (1) {
		if ((directory in is_package) && package_module[directory] == module) {
			return directory
		}
		if (directory == module || directory == ".") {
			return ""
		}
		directory = dir_name(directory)
	}
}

function decide(key,   is_root, edges, index_, has_go, has_pnpm, is_unknown, root_count, roots, reason, queue, head, tail, current, neighbours, neighbour_count, neighbour_index, visited, pattern, base, is_wildcard, package, matched, name, members, member_count, member_index, project) {
	has_go = 0
	has_pnpm = 0
	is_unknown = 0
	reason = ""
	head = 1
	tail = 0
	for (index_ = 1; index_ <= target_count; index_++) {
		if (target_key[index_] != key) {
			continue
		}
		pattern = target_pattern[index_]
		matched = 0
		if (target_ecosystem[index_] == "go") {
			has_go = 1
			if (substr(pattern, 1, 7) == "import:") {
				pattern = substr(pattern, 8)
				is_wildcard = pattern ~ /\/\.\.\.$/
				if (is_wildcard) {
					pattern = substr(pattern, 1, length(pattern) - 4)
				}
				base = resolve_import(pattern)
			} else {
				pattern = substr(pattern, 5)
				is_wildcard = pattern == "..." || pattern ~ /\/\.\.\.$/
				if (pattern == "...") {
					base = "."
				} else if (is_wildcard) {
					base = clean_path(substr(pattern, 1, length(pattern) - 4))
				} else {
					base = clean_path(pattern)
				}
			}
			if (base != "") {
				for (package in is_package) {
					if (package == base || (is_wildcard && is_within(base, package))) {
						if (!(("g" SUBSEP package) in visited)) {
							visited["g" SUBSEP package] = 1
							queue[++tail] = "g" SUBSEP package
						}
						is_root[package] = 1
						matched = 1
					}
				}
			}
		} else if (target_ecosystem[index_] == "pnpm") {
			has_pnpm = 1
			if (substr(pattern, 1, 5) == "name:") {
				name = substr(pattern, 6)
				if (name in project_by_name) {
					member_count = split(project_by_name[name], members, SUBSEP)
					for (member_index = 1; member_index <= member_count; member_index++) {
						project = members[member_index] + 0
						if (!(("p" SUBSEP project) in visited)) {
							visited["p" SUBSEP project] = 1
							queue[++tail] = "p" SUBSEP project
						}
						matched = 1
					}
				}
			} else {
				base = clean_path(substr(pattern, 5))
				if (base != "" && (base in project_index_by_dir)) {
					project = project_index_by_dir[base]
					if (!(("p" SUBSEP project) in visited)) {
						visited["p" SUBSEP project] = 1
						queue[++tail] = "p" SUBSEP project
					}
					matched = 1
				}
			}
		}
		if (!matched) {
			is_unknown = 1
			printf "[ankra] affected: stage %s names %s %s, which matches nothing in this repository, so the stage runs\n",
				key, target_ecosystem[index_], substr(target_pattern[index_], index(target_pattern[index_], ":") + 1) > "/dev/stderr"
		}
	}
	if (is_unknown) {
		return
	}

	if (has_go && go_everything != "") {
		reason = go_everything
	}
	if (has_pnpm && pnpm_everything != "" && reason == "") {
		reason = pnpm_everything
	}
	while (reason == "" && head <= tail) {
		current = queue[head++]
		if (substr(current, 1, 1) == "g") {
			package = substr(current, 3)
			if (package in changed_package) {
				reason = changed_package[package]
				break
			}
			if (package_module[package] in changed_module) {
				reason = changed_module[package_module[package]]
				break
			}
			edges = (package in go_edges) ? go_edges[package] : ""
			if ((package in is_root) && (package in go_test_edges)) {
				edges = edges (edges == "" ? "" : SUBSEP) go_test_edges[package]
			}
			if (edges == "") {
				continue
			}
			neighbour_count = split(edges, neighbours, SUBSEP)
			for (neighbour_index = 1; neighbour_index <= neighbour_count; neighbour_index++) {
				if (!(("g" SUBSEP neighbours[neighbour_index]) in visited)) {
					visited["g" SUBSEP neighbours[neighbour_index]] = 1
					queue[++tail] = "g" SUBSEP neighbours[neighbour_index]
				}
			}
		} else {
			project = substr(current, 3) + 0
			if (project in changed_project) {
				reason = changed_project[project]
				break
			}
			if (!(project in project_edges)) {
				continue
			}
			neighbour_count = split(project_edges[project], neighbours, SUBSEP)
			for (neighbour_index = 1; neighbour_index <= neighbour_count; neighbour_index++) {
				if (!(("p" SUBSEP neighbours[neighbour_index]) in visited)) {
					visited["p" SUBSEP neighbours[neighbour_index]] = 1
					queue[++tail] = "p" SUBSEP neighbours[neighbour_index]
				}
			}
		}
	}

	if (reason != "") {
		printf "ankra.affected.%s=true\n", key >> out
		printf "[ankra] affected: stage %s runs: %s changed and is in its dependency graph\n", key, reason > "/dev/stderr"
	} else {
		printf "ankra.affected.%s=false\n", key >> out
		printf "[ankra] affected: stage %s is not affected: no changed file is in its dependency graph (%d packages and projects)\n", key, tail > "/dev/stderr"
	}
}
